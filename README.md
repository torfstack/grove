# Grove

A Google Drive sync client written in Go. Linux is the first supported
platform; Windows and macOS are planned.

The planned `grove` CLI performs individual operations such as authentication,
sync, and status. A later `groved` daemon schedules the same shared sync engine.
Authentication, disposable fixture tooling, and initial download sync are implemented.
Status reporting, incremental/two-way sync, and the daemon are planned.

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
`mise.toml`; live Drive tests are separate.

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

## Initial download sync

Download an account-owned My Drive folder into an absent or empty directory:

```sh
./bin/grove sync \
  --profile-dir "$HOME/.local/state/grove/my-initial-profile" \
  --remote-root "$GROVE_REMOTE_ROOT" \
  --local-dir "$HOME/grove-download" \
  --token-file "$HOME/.config/grove/token.json"
```

Set `GROVE_REMOTE_ROOT` locally to the selected folder ID. Keep profiles outside
Git and separate from the destination and token. The profile binds those paths
and the remote root; use the same arguments to resume or repeat a run. Files are
verified and published without replacing existing files. An unchanged second run
rehashes local files and reads remote metadata, without downloading media again.

This version populates ordinary files and folders. It rejects Google-native
files, shortcuts, duplicate sibling names, third-party-owned content, shared
drives, symlinks, and incompatible names. Local edits and remote changes after
completion fail safely; nothing is overwritten or deleted. This is initial
population, not ongoing synchronization. Interrupted transfers restart from
zero while completed files are preserved.

Profiles and destinations are exclusively locked. Overlapping destinations are
registered in `$XDG_STATE_HOME/grove/destinations` (default
`~/.local/state/grove/destinations`). Registrations retain profile ownership;
there is no reset command yet. Keep the profile and registry records together
rather than deleting them to bypass a conflict.

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

The explicitly enabled test seeds a fresh remote folder, inspects it, downloads
and independently verifies its content, repeats with zero media transfers, and
trashes only that run's owned fixture objects. Tokens and generated IDs remain
in private records outside Git. Successful runs retain local records; failed
runs also retain remote data for inspection. No token needs to be pasted into
chat or added to CI. Default tests never load live credentials.

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
