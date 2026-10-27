# Grove

A Google Drive sync client written in Go. Linux is the first supported
platform; Windows and macOS are planned.

The `grove` CLI performs individual operations such as authentication, sync, and
status. The Linux `groved` daemon schedules the same shared sync engine.
`grove-tray` displays offline status in compatible Freedesktop trays, including
Waybar, with optional validated systemd user-service controls.
Authentication, disposable fixture tooling, incremental downloads, explicit
two-way uploads, safe deletion propagation, conflict controls, and upload
recovery commands, periodic scheduling, opt-in local watching, and offline daemon/profile status are
implemented.

Development tools are pinned in [mise.toml](mise.toml). Install them with
`mise install`.

Build and test:

```sh
mise run build
mise run test
mise run test-race
mise run vet
mise run lint
```

Formatting uses `mise run fmt` (gofmt). The pinned golangci-lint checks formatting,
unchecked errors, suspicious code, unused code, and ineffective assignments.
Its explicit rule set is in `.golangci.yml` and includes test files.

GitHub Actions runs lint/formatting checks, offline tests, race tests, and a build
on Linux for pull requests and pushes to `main`. It uses the versions in
`mise.toml`; live Drive tests are separate. Offline Linux tray tests also require
`dbus-daemon` and run on private session buses.

CodeQL analyzes Go and Actions workflows on PRs, main pushes, and a weekly schedule.
Dependency review rejects newly introduced known high or critical vulnerabilities.
Dependabot checks Go modules and Actions weekly, grouping minor/patch updates and
limiting routine PRs to three per ecosystem. These Dependabot schedules become
active when the configuration reaches `main`.

GitHub secret scanning and push protection are enabled. CodeRabbit's GitHub app
is installed and configured for advisory reviews of PRs targeting `main`.

Authenticate with a Google Desktop app client JSON:

```sh
./bin/grove auth --client-secret ~/google_client_secret.json
```

This opens the system browser and requests read-only Drive access. To allow
fixture creation and future two-way operations, explicitly request write access:

```sh
./bin/grove auth --client-secret ~/google_client_secret.json --access read-write
```

The command also prints a URL you can open manually on the same machine if browser
launch fails. It waits up to five minutes; Ctrl-C cancels. Use the dedicated test
account for development.

Tokens are plaintext files with owner-only permissions. The default path is
`$XDG_CONFIG_HOME/grove/token.json` (or `~/.config/grove/token.json`) on Linux and
`~/Library/Application Support/grove/token.json` on macOS. Use `--token-file PATH`
to isolate a test run. Credentials remain at their original location; moving
them will require updating configuration or authenticating again. Every auth
invocation requests consent again and replaces the token only after success.
Auth and Drive commands lock the canonical token path exclusively. Successful
refreshes are persisted atomically; another command using that token fails while
the lock is held.

Google External apps in Testing mode issue Drive refresh tokens that expire after
seven days; rerun auth when needed. Windows auth is not implemented yet.

Start with [project status](docs/STATUS.md), [product scope](docs/PRODUCT.md),
[architecture](docs/ARCHITECTURE.md), and [roadmap](docs/ROADMAP.md).
The [Drive testbed](docs/TESTBED.md) describes live testing.

## Download sync

Download an account-owned My Drive folder into an absent or empty directory.
Its parent directory must already exist:

```sh
./bin/grove sync \
  --profile-dir "$HOME/.local/state/grove/my-initial-profile" \
  --remote-root "$GROVE_REMOTE_ROOT" \
  --local-dir "$HOME/grove-download" \
  --token-file "$HOME/.config/grove/token.json"
```

Set `GROVE_REMOTE_ROOT` locally to the selected folder ID. Keep profiles outside
Git and separate from the destination and token. The profile binds those paths
and the remote root; use the same arguments to resume or repeat a run. New files are
verified and published without replacing existing files. Tracked unchanged files
can be replaced by verified remote updates. An unchanged second run
rehashes local files and reads remote metadata, without downloading media again.

This version populates ordinary files and folders. It rejects Google-native
files, shortcuts, duplicate sibling names, third-party-owned content, shared
drives, symlinks, and incompatible names. After initial population, the same command downloads remote additions and updates
and follows safe file/folder renames and moves. Local edits, unexpected local
entries, remote removals, type changes, occupied move targets, and cyclic
rearrangements stop preflight and preserve content. A missing tracked path still
stops the default download-only path. On a populated profile, explicit
`sync --two-way` treats missing tracked files and folders as deletion intent and
moves unchanged remote items to Drive Trash. It verifies a deleted folder's full
tracked subtree and trashes the topmost folder once. Changed or expanded remote
subtrees stay in Drive and become explicit deletion conflicts. Use
`conflicts show <id>` and then `conflicts resolve <id> --keep local|cloud` to
choose whether to trash the reviewed remote tree or restore it locally. Configured
automatic conflict policies do not choose for deletion conflicts. Trash is
reversible in Drive; Grove never permanently deletes data. The sync summary's
`Trashed` count includes descendants moved with a folder. Interrupted
transfers restart from zero; completed progress is retained.

If cloud-side deletion conflict restoration is interrupted, resume with
`sync` without `--two-way`. The profile stays incomplete until restoration
finishes; other pending conflicts can still be inspected and explicitly resolved.

The private `<profile-dir>/state.json` stores remote identities/versions, local
SHA-256 hashes, and recovery intent. Every run rehashes local files; timestamps
are not the baseline. Existing version-1 profiles recover pending initial work
before atomically upgrading to version 2. Explicit two-way sync records the
additional revision and conflict metadata in version 3. Older binaries cannot
read upgraded profiles; keep their state rather than deleting it to downgrade.

Replacement retains old bytes at a journal-owned backup path until the new
baseline is durable. A target can briefly be absent between backup and publication;
recovery finishes the operation or stops on unexpected content. Moves require
native no-replace rename support on the destination filesystem (Linux and macOS
boundaries are implemented). Capability/name preflight occurs before tracked
mutations. Locks exclude other Grove writers; arbitrary concurrent edits by other
applications remain outside that guarantee. Drive traversal is not a transactional
whole-account snapshot.

Profiles and destinations are exclusively locked. Filesystem leases and shared
ancestor locks also exclude aliases and nested writers across registry locations.
A private lock file is retained beside each destination. Overlapping destinations are
registered in `$XDG_STATE_HOME/grove/destinations` (default
`~/.local/state/grove/destinations`). Registrations retain profile ownership;
there is no reset command yet. Keep the profile and registry records together
rather than deleting them to bypass a conflict.

## Two-way sync and conflict recovery

On a populated profile, `sync --two-way` explicitly enables local additions,
content uploads, and safe local deletion propagation using read-write
authorization. Conflict controls are available
through `config set conflict-policy ask|keep-local|keep-cloud`,
`conflicts list`, `conflicts show <id>`, and
`conflicts resolve <id> --keep local|cloud`. Use `conflicts show` to see the local
candidate at ordinal 0 and cloud candidate ordinals, sizes, and known timestamps.
Select a particular cloud revision with `conflicts resolve <id> --candidate 2`;
local selection remains `--keep local`. Multi-candidate conflicts require
explicit selection even when the profile has an automatic conflict policy. Each
command uses the same profile binding flags as sync. Uploads preserve revisions
because Drive does not enforce conditional writes; permanent pins consume its
per-file quota.

If an upload is interrupted, inspect the recorded transaction offline:

```sh
./bin/grove recovery inspect \
  --profile-dir "$GROVE_PROFILE_DIR" --remote-root "$GROVE_REMOTE_ROOT" \
  --local-dir "$GROVE_LOCAL_DIR" --token-file "$GROVE_TOKEN_FILE"
```

The report shows the recorded operation, phase, verified stage availability,
competitor count, and next actions. It does not contact Drive or establish the
current remote outcome. Retry just that transaction with read-write access:

```sh
./bin/grove recovery retry \
  --profile-dir "$GROVE_PROFILE_DIR" --remote-root "$GROVE_REMOTE_ROOT" \
  --local-dir "$GROVE_LOCAL_DIR" --token-file "$GROVE_TOKEN_FILE"
```

Retry can finish recording proven success, preserve a competing revision as a
pending conflict, or finish owned cleanup. It does not start other sync work,
send file content, create objects, or apply the profile's conflict policy. A
`conflict-pending` outcome requires explicit conflict resolution afterward.
Download/move/replace/trash recovery remains with the normal `sync` command.

To retrieve an available verified snapshot without changing the transaction:

```sh
./bin/grove recovery export --candidate local --output "$GROVE_EXPORT_FILE" \
  --profile-dir "$GROVE_PROFILE_DIR" --remote-root "$GROVE_REMOTE_ROOT" \
  --local-dir "$GROVE_LOCAL_DIR" --token-file "$GROVE_TOKEN_FILE"
```

Set these placeholders locally to the existing binding and a new output file.
The output parent must already exist, outside profiles, the destination registry,
and registered sync destinations. Export is an offline copy with mode 0600;
existing output files are never replaced. `--candidate cloud` requires a fully
verified cloud capture. Partial captures and folder creates have no exportable
bytes. Export of ordinary conflict records is outside this command's scope.
After a publication error, an output file may exist; inspect it before choosing
another output path. A crash can leave a private partial temporary export.

An unchanged remote head or missing allocated create ID cannot prove that an
uncertain request has settled. Such profiles may remain blocked indefinitely;
export retrieves verified bytes but does not permit safely resetting the profile.
Multiple recorded competitors remain preserved and blocked until every candidate
is captured. Once the conflict is published, choose a cloud ordinal explicitly.
Resolved revisions remain pinned because Drive does not allow reclaiming those
pins after they become non-head. Keep journals, stages and registry records rather
than deleting them to force progress.

## Live fixture test

Use only the dedicated disposable Google account. Authenticate once locally:

```sh
mise run build
./bin/grove auth \
  --client-secret "$HOME/google_client_secret.json" \
  --access read-write \
  --token-file "$HOME/.config/grove-test/token.json"

GROVE_LIVE_TEST=1 \
GROVE_TEST_TOKEN_FILE="$HOME/.config/grove-test/token.json" \
GROVE_TEST_RUNS_DIR="$HOME/.local/state/grove-test/runs" \
  mise run test-live
```

The explicitly enabled suite seeds a fresh remote folder, inspects it, downloads
and independently verifies its content, repeats with zero media transfers, and
trashes only each run's owned fixture objects. The incremental scenario also applies
owned remote additions, content updates, and file/folder moves before independently
verifying a second sync and its no-op repeat. Tokens and generated IDs remain
in private records outside Git. Successful runs retain local records; failed
runs also retain remote data for inspection. No token needs to be pasted into
chat or added to CI. Default tests never load live credentials.

Both paths must be outside every ancestor directory containing a `.git` entry.
If your home contains `.git` solely for global hooks, the conservative guard also
rejects these example paths. Use private paths outside that directory instead.

For a retained run, use its reported directory:

```sh
./bin/grove fixture inspect --run-dir "$GROVE_FIXTURE_RUN_DIR" \
  --token-file "$HOME/.config/grove-test/token.json"
./bin/grove fixture cleanup --run-dir "$GROVE_FIXTURE_RUN_DIR" \
  --token-file "$HOME/.config/grove-test/token.json"
```

You can also seed manually with `fixture seed --manifest
 testdata/fixtures/baseline/manifest.json --run-dir PATH --token-file PATH`, and
verify downloaded content with `fixture verify --manifest
 testdata/fixtures/baseline/manifest.json --local-dir PATH`. See command help for
required flags. Cleanup refuses unrecorded descendants or changed ownership.

## Periodic sync on Linux

Build with `mise run build`. Initialize a profile with `grove sync` first; the
daemon uses that profile's saved remote root, destination, and token binding.
Save a private JSON file outside Git and the synced destination, using absolute
paths (replace the example profile path):

```json
{
  "version": 1,
  "profile_dir": "/home/you/.local/state/grove/profiles/example",
  "mode": "download",
  "interval": "5m",
  "watch_local": false
}
```

```sh
chmod 600 /absolute/path/daemon.json
./bin/groved --config /absolute/path/daemon.json
# In another terminal:
./bin/grove status --profile-dir /absolute/path/profile
./bin/grove status --profile-dir /absolute/path/profile --json
```

`groved` stays in the foreground. Ctrl-C or SIGTERM cancels active work, waits
for cleanup, and releases ownership. It attempts sync immediately, then waits
the full interval after each attempt finishes when watching is disabled. Omit `interval` for five minutes;
the minimum is one second. Restart to apply config changes. `mode` is required:
`download` permits remote downloads; `two-way` explicitly enables the existing
upload and deletion behavior and requires a fully populated profile.

Set `watch_local` to `true` to react to local changes as well as periodic scans.
Watching requires an existing destination directory and Linux `/proc/self/fd`.
Changes wait for one quiet second, capped at five seconds from the first hint;
changes during sync request one follow-up. Grove's own writes can cause a no-op
follow-up, but unchanged scans settle back to waiting. Event-driven attempts do
not postpone periodic remote scans. Busy/failed attempts restrict event retries
for one interval; conflicts and uncertain writes retain their operator workflow.
Download mode still rejects unsupported local changes; watching does not enable
uploads. Root replacement, watch limits, permission failures, or unrecoverable
watch coverage errors stop the daemon visibly. Restart after addressing the cause.
Native events are best effort, particularly on network/unsupported filesystems;
periodic full scans remain necessary. Overflow repairs coverage and requests a
full sync without authorizing deletions from missing events.

Add `"remote_poll_interval": "30s"` to the daemon JSON to check Drive's changes
feed between full scans. Omit it to disable remote polling; the minimum is ten
seconds. It works with either mode and with or without local watching. Restart
to apply the setting. Any My Drive change can request a normal sync, including
changes outside this profile or Grove's own writes, so occasional extra no-op
runs are expected. Full periodic scans continue unchanged.

Remote checks use read-only authorization and serialize with this daemon's sync
work, including cleanup, so they do not compete for its own token/profile lease.
A check has a ten-second context budget; a sync may wait for an already active
check to finish. Other processes retain normal token/profile exclusion. Polling
cursors live only in memory; initialization requests a full sync after the cursor
is captured. Authentication, busy, network, and feed errors retry at the remote
interval and appear in historical status; periodic sync still runs. If the
provider rejects a cursor repeatedly, restart the daemon to obtain a new one.
Genuine session/lease cleanup failures stop visibly for inspection.

Feed notifications never apply operations directly. Missing or incomplete
notifications never authorize deletion. Remote removals keep the engine's
existing preflight/conflict behavior and preserve local data. Status reports the
configured interval and last completed remote-check outcome, not current health;
`next_attempt_at` continues to describe a normal sync, not a cheap feed check.

Status observes the lifetime lock independently of its **last recorded** phase,
result, and schedule. History may be stale after a crash. The recorded `watch_local` flag is
configuration history, not a watcher-health check; `next_attempt_at` is the
earliest currently scheduled deadline and may change with local events. A busy profile has no
available summary. Status requires no credentials or network access. Another
daemon for the profile is rejected; manual commands and other profiles sharing
a token retain their normal per-attempt exclusion.

Conflicts, expired authentication, and uncertain writes retain the engine's
existing safety rules. Inspect status and the existing conflict/recovery commands;
stop the daemon before making operator repairs, reauthenticate when necessary,
and restart. Event retries wait a full interval after busy or failed attempts; periodic
reconciliation keeps its existing deadline. The daemon
does not resolve conflicts implicitly. Ownership/record/cleanup failures stop
it with a nonzero exit. A [systemd user-service template and installation
guide](contrib/systemd/README.md) supports background operation of existing
profiles. Hot reload and non-Linux daemon runtime remain future work.


## Linux desktop tray

After building, launch an indicator for your existing profile:

```sh
./bin/grove-tray --profile-dir /absolute/path/to/profile --name Personal
```

Enable the `tray` module in your Waybar right-hand modules if it is not already
present. The indicator shows daemon ownership, last recorded sync, and available
conflict/recovery information. It stays visible when the daemon is stopped;
Quit tray closes only the indicator. Status inspection needs no credentials or
network connection. A completed attempt is historical, not proof the remote
is currently synchronized.

Optionally append `--systemd-instance personal` for start/stop controls targeting
`groved@personal.service`. Controls require the unit's effective direct command
and private config to match the selected profile. Starting the indicator never
starts a service automatically. See the [desktop guide](contrib/desktop/README.md)
for installation, service linkage, icons, and opt-in session autostart.
Waybar 0.15.0 is verified; another independent tray host remains untested.
