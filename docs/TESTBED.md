# Drive testbed

The agreed approach is a dedicated disposable Google account plus a versioned
fixture manifest. The baseline workflow is merged into main and passed live acceptance on macOS.
The incremental scenario is implemented on `feat/incremental-download`; dedicated-
account live acceptance passed on macOS on 2026-10-10.

On Linux x86_64 on 2026-10-22 at `8123f12`, the full live suite completed in
270.050 seconds with one initial-sync failure: Drive's metadata version changed
during download while identity, parents, size/hash, ownership, and active status
still matched. All other live scenarios passed, including deletion, incremental
sync, revision pinning, conditional-write probing, and upload/concurrent revision
recovery. The retained fixture passed independent inspection and owned cleanup.
A fresh initial-sync retry passed in 38.686 seconds and cleaned its fixture.
Linux execution is now demonstrated for every live scenario, but the aggregate
suite is not fully green; the intermittent initial-download consistency check
remains unresolved.

The metadata-version fix from spec 000008 subsequently passed the full Linux
live suite on 2026-10-22 in 279.377 seconds. Initial sync, incremental sync,
revision pinning, stale-write probing, two-way upload/concurrent revision
recovery, and local deletion all passed with fixture-owned cleanup. Formatting,
lint, full offline/race tests, and the native Linux build also passed. The
version-only failure is covered deterministically for transfers and disk-reloaded
recovery; the original server-side trigger remains unknown. A subsequent fresh
full-suite run for PR #13 using the dedicated local token also passed in
275.451 seconds with fixture-owned cleanup. Uncached offline/race tests, lint,
and the native Linux build passed again before PR review.

## Google setup (user)

1. Create a Google account holding only disposable test data.
2. Create a Cloud project and enable the Google Drive API.
3. Configure OAuth as External / Testing and add that account as a test user.
4. Create a Desktop app OAuth client for the initial Linux target and download
   its JSON credentials. Store them outside Git.

Use browser-based user OAuth, with a locally stored refresh token. The account
password is never required by Grove or the assistant. Implement a desktop
loopback authorization flow; the quickstart's sample authentication code is not
the specification for our production flow.

Read-only initial download tests can use `drive.readonly`; two-way operations
need `drive`. `drive.file` does not grant access to arbitrary existing content.
OAuth scopes cannot restrict the full Drive scope to our fixture folder; that
boundary must be enforced by the test harness. External / Testing refresh tokens
using Drive scopes expire after seven days, so reauthentication is expected.

Sources: [Go quickstart](https://developers.google.com/workspace/drive/api/quickstart/go),
[Drive scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth),
[token expiration](https://developers.google.com/identity/protocols/oauth2#expiration).

## Repeatable fixtures

Commit a manifest with logical entry IDs, parent relationships, Drive names,
entry types, deterministic content, and content hashes. Use relationships rather
than paths as identity because Drive permits duplicate sibling names.

Each seed run creates a fresh remote root and a local run record mapping logical
IDs to actual Drive IDs. Generated IDs, tokens, and run state stay outside Git.
Local downloads use a fresh temporary directory and a dedicated Grove profile.

Start with a portable baseline: nested and empty folders, zero-byte files, text,
binary content, and Unicode names. Verify membership, sizes, and content hashes;
do not require identical timestamps. Run sync twice and verify the second pass
does no unnecessary work.

Keep duplicate sibling names in a separate edge-case fixture until name mapping
is specified. Later add incompatible names, shortcuts, Google-native documents,
sharing, remote changes, and concurrent edits with explicit expected outcomes.

Seeding, inspection, verification, and cleanup are separate operations. Cleanup
targets only objects recorded as belonging to that run; it must never search by
folder name alone or clear the account wholesale. Failed runs retain diagnostic
records without credentials for investigation.

## Test layers

- Fast offline tests cover manifest validation, planner decisions, simulated
  network failures, and recovery behavior.
- Live tests are explicitly enabled, exercise the real Drive API, and verify
  fixtures using expected content rather than the sync engine's own conclusions.
- Live tests are not part of the default offline test suite. Public CI receives
  no test account credentials by default.

## Completion criteria for the first testbed implementation

Authenticate as the test account, seed the baseline fixture, download it to an
empty local directory, verify expected content, and repeat the sync without
unnecessary transfers. Record failures and preserve enough run state to inspect
them. Command syntax and private records are defined in
[spec 000002](spec/000002-initial-sync-design.md) and implemented by the paired plan.

Run `mise run test-live` with `GROVE_LIVE_TEST=1`, an absolute dedicated
`GROVE_TEST_TOKEN_FILE`, and an absolute `GROVE_TEST_RUNS_DIR` outside Git. The
suite uses page size 2, seeds a new root, independently verifies content, checks
zero second-run media requests and unchanged local mtimes, then cleans up on
success. Failed runs retain their private records and remote data. Offline tests
cover interruption/recovery and use a local HTTP Drive server for the full CLI
workflow. Public CI does not receive credentials. See README for executable setup.

`TestConditionalUploadConflictLive` probes Drive's stale-write behavior using
fixture-owned content. On 2026-10-10 it found no HTTP ETag in the Drive v3
metadata response. A matching-version resumable update succeeded, but a second
update using that now-stale version in `If-Match` also returned HTTP 200 and
replaced the competing content. The test independently verified the change,
restored the competing bytes, and cleaned up the fixture. A server-enforced
condition is unavailable, so existing-file uploads use the approved
preflight-and-revision-recovery design documented below.

A fixture-owned live spike uploaded stale content after a competing edit, found
the competing bytes in Drive's revision list, marked that revision `keepForever`,
and downloaded it for independent verification. A focused live test also
confirmed that the current head can be pinned and unpinned before an update.
End-to-end upload acceptance passed: Grove uploaded a local edit, uploaded a
later local edit, recovered a remote write injected during an upload, verified
the captured revision bytes, and resolved the conflict by installing and
uploading the cloud candidate.

After PR review fixes on 2026-10-11, initial sync, incremental sync, current-head
pinning, and upload/conflict recovery passed again. The aggregate live suite
failed in the separate conditional-write probe's metadata read. A standalone
probe retry reached cleanup but encountered a transport error; retained fixtures
were cleaned successfully through the ownership-checked CLI. The aggregate live
command is not fully green.

On macOS on 2026-10-12, a fresh full-suite run passed incremental sync,
current-head pinning, conditional-write probing, and two-way upload/concurrent
revision recovery. Initial sync failed its post-download consistency check:
Drive returned the same file identity, parents, content, ownership, and trash
state, but a different metadata version. The failed fixture was cleaned with
the ownership-checked CLI. The conditional-write probe again showed that Drive
accepted a stale version condition with HTTP 200; the probe restored the
competing content and cleaned its fixture. Initial-sync live verification
remains unresolved.

The live run found that the initial revisions request must omit an empty
`pageToken`, and Drive rejects unpinning a revision after it becomes non-head
(`illegalKeepForeverModification`). Therefore successful updates and recovered
conflict revisions keep their pins. Drive allows at most 200 pinned revisions per
file, so repeated uploads consume that quota and further uploads stop safely when
it is full. Pin operations may also increment metadata `version` without changing
the head content revision; Grove treats that version-only change as unchanged
when revision ID, modified time, size, MD5, identity, and path match. Multiple
possible racing revisions remain journaled for manual recovery.

## Incremental acceptance

`testdata/fixtures/baseline/incremental.json` retains logical identities while
renaming a folder, renaming/updating a file, moving a file into a descendant
folder, and adding a folder/file. Payloads reuse committed baseline bytes.
The fake-HTTP CLI workflow applies these changes through owned fixture tooling,
inspects remote content, syncs, independently verifies the target manifest,
repeats without media transfers, and cleans up child-first.

`TestIncrementalSyncLive` runs the same scenario under existing live gates and
private run/session locks. Failed mutation records retain intent; inspect can
reconcile confirmed owned updates, and cleanup checks actual recorded parents.
Neither default tests nor public CI load live credentials. The incremental live
scenario passed on macOS on 2026-10-10 in 83.43 seconds; initial live acceptance
also passed in 41.06 seconds. Both runs completed owned remote cleanup.
Crash recovery is tested deterministically
with real local files and journal-save faults; kernel fsync failure is not
independently injected at every transition. Linux runtime evidence comes from
Linux execution/CI, not the cross-build.

## Daemon acceptance on Linux

The opt-in live suite includes `TestDaemonPeriodicDownloadLive` and
`TestDaemonPeriodicUploadLive`. Each passes the dedicated-token gate before
building actual binaries, seeds its own fixture, initializes a disposable profile,
and runs `groved` in the foreground with an isolated destination registry.
Download acceptance coordinates a fixture mutation through the same token lease
and observes a busy daemon attempt. Upload acceptance independently reads Drive
bytes after a local edit. Both verify a later no-op cycle, unchanged local mtimes,
offline command status, SIGTERM exit, inactive ownership, and owned cleanup.
Bounded token-acquisition retries stop once fixture work begins; uncertain
mutations are never retried. Failed runs retain private process/status/fixture
records. Stop the process before inspecting or cleaning its recorded fixture.

Offline process tests use a test-only fake HTTP Drive with real token/profile/
destination leases. They exercise active and idle signal cancellation, a second
owner through an alias, manual sync/recovery contention, shared-token profiles,
forced process death, and restart recovery. Production has no fake endpoint flag.

## Systemd user-service acceptance

Spec 000010 acceptance ran on Linux with systemd 262.1 on 2026-10-24.
`mise run build` and the complete offline `mise run test` passed. Sandbox-only
attempts could not create Go cache entries, inspect some filesystem paths, or
open loopback listeners; the successful offline run used a temporary Go cache
and approved execution outside the sandbox. No Go code changed.

`systemd-analyze --user verify contrib/systemd/groved@.service` reported only
the expected absent `/usr/bin/groved` on this source-build machine. Validation
of a temporary copy of the template with a real executable override passed
without diagnostics. The copied template retained its shipped lifecycle options.

Isolated, uniquely named runtime-only user units exercised a harmless Python
helper. Manager properties confirmed Type=exec, Restart=no, SIGTERM,
KillMode=control-group, unlimited stop timeout, umask 0077, and journal output.
Actual argv checks verified home/config expansion, executable and config paths
with spaces, and a literal escaped instance containing `\\x2f` without converting
it to a directory separator. A journal marker was observed. A delayed SIGTERM
handler completed before clean service inactivity; a deliberate helper failure
remained failed with zero automatic restarts.

A separate runtime unit launched the built actual `groved` against a private
disposable `{}` config. It exited 1, reported the fixed invalid-version error in
the journal, and stayed failed with zero automatic restarts. Validation stopped
before profile, token, or network access. Every test-owned unit, runtime link,
drop-in, and disposable state directory was removed; no unit was enabled and
lingering was not changed.

The exact source-install shell block ran in a temporary synthetic home containing
spaces. The JSON was mode 0600; a second run refused existing files and preserved
all file hashes. Existing real-daemon tests passed with `-count=1`:
`TestGrovedProcessOwnershipAndSignals`, `TestGrovedProcessesShareToken`, and
`TestGrovedSignalDuringFolderCreation`. These use fake HTTP and actual process
signals/locks. Helper tests establish manager mechanics, not real sync;
authenticated daemon sync under systemd was tested separately below. Logout,
boot, and linger behavior remain untested. Existing opt-in foreground live
acceptance remains separate.

`TestDaemonSystemdDownloadLive` adds a separately gated systemd acceptance. Run
it with `GROVE_SYSTEMD_LIVE_TEST=1` in addition to the standard live-test gate,
dedicated token, and private runs path. It requires a running user manager whose
unit search path includes `~/.config/systemd/user`. The test uses a unique
instance, creates a source template there only when absent, and writes only its
own per-instance override and private config. It never enables the unit or
changes lingering. After the test it stops the unit and removes only paths it
created.

Authenticated service acceptance passed three times on 2026-10-25 (67.74s,
67.51s, and 67.02s), then three times on the PR branch on 2026-10-26 (67.77s,
68.06s, and 71.14s). Each run
built actual Grove binaries and initialized a disposable profile,
started `groved` through the user service template, applied an owned remote
fixture change, independently verified the download, observed a no-op cycle,
checked offline status while active and inactive, and stopped the service
gracefully. The test-only drop-in sets a 15-second stop bound and asserts that
the running manager loaded it. Fixture-owned remote data was cleaned by the test; its private local
run directory was removed after each successful run. Tokens and generated IDs
were not printed.

The independent review found a valid custom-XDG_CONFIG_HOME installation issue:
the original guide copied units to ~/.config/systemd/user even when that path
was absent from the manager's search path. An executable guide-block check
reproduced the missing unit under a custom config home before the fix. The guide
now uses `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user` consistently for units
and overrides, while keeping Grove JSON at the approved fixed home-relative
path. Default and custom config-home setup/repeat checks both pass after the
fix, including spaces in both paths. The shell and user-manager XDG environment
must agree. PR #16's completed CodeRabbit review identified that the inspection
must query the running manager, rather than systemd-analyze's local path list.
The guide now uses `systemctl --user show -p UnitPath --value` and requires the
shell-derived unit directory to match a reported path. The command succeeded
both normally and with a shell-only custom XDG_CONFIG_HOME, reporting the same
running-manager paths; installed systemd-analyze documentation confirms this
distinction. New directories require daemon-reload before rechecking.

## Local watching acceptance

Spec 000011 adds strict watch_local config and historical status. Deterministic
clock tests cover debounce, five-second cap, periodic fairness, busy/failure retry
limits, completion-boundary edits and historical deadlines. Native Linux tests
cover nested/atomic saves, moved-in directories, metadata/removal hints, read-only
silence, actual directory-to-symlink replacement, root identity loss, injected
overflow, descriptor churn, transient enumeration loss, bounded hints and cleanup.

Actual daemon subprocesses with fake Drive verify downloaded changes settle
after a no-op follow-up, edits during blocked transfers survive and are observed,
SIGINT/SIGTERM cancellation, fatal root replacement, and released lifetime locks.
Repeated race-enabled watcher process tests pass. A real transient validation-
probe directory disappearing during enumeration reproduced a coverage failure;
a deterministic retry regression and the process scenario now pass.

`TestDaemonLocalWatchUploadLive` is added behind the existing explicit dedicated-
account gates. It runs actual binaries with watch_local=true and a ten-minute
interval, observes local-edit upload before a 90-second observation timeout,
independently verifies Drive content, requests metadata-triggered no-op cycles,
checks stable content/mtimes and offline status, and cleans fixture-owned data.
Actual dedicated-account acceptance passed on 2026-10-25 in 42.888s; the upload
was independently verified in 7.50s. The first run exposed a harness race that
waited for transient upload counters; a following no-op could replace them.
The corrected test requires a completed post-edit attempt and independent Drive
bytes. Both runs finished fixture-owned cleanup, with no pending uncertain write.
No-op stability, offline status and SIGTERM shutdown passed. Native watcher evidence is local Linux filesystem
coverage, not a guarantee for network filesystems or authenticated systemd sync.


## Remote changes acceptance

Spec 000012 adds opt-in My Drive changes-feed hints without replacing complete
periodic scans. Fake HTTP and locked-service tests cover opaque cursor handling,
full pagination, partial/error retention, bounds, cancellation and cleanup.
Deterministic daemon tests cover initialization after startup, check/sync gate
serialization, busy/failed retries, valid historical record transitions and late
faults during shutdown. Real processes cover remote-only detection before a
10-minute periodic deadline, idle checks preserving prior attempt counts, combined
sources, signal cancellation during a check and lifetime lease release.

Dedicated-account actual-binary acceptance passed on 2026-10-25: remote download
73.73s, independently verified 12.40s after owned remote mutation completion;
watcher regression 41.96s, independently verified upload in 7.30s. Combined run
115.697s. Remote mode waits for post-cursor startup reconciliation and no pending
debounce before mutation, excluding startup work as the download trigger. Both
scenarios verify no-op content/mtime stability, offline configuration/status,
SIGTERM and owned cleanup. Remote live mode enables local watching to request
later no-op cycles; remote-only behavior has separate process evidence. Network
filesystem guarantees and authenticated systemd boot/logout remain unclaimed.


The shared helper's periodic regressions also passed after the changes: download
68.34s and upload 42.30s, combined 110.647s. Both verified content, no-op cycles,
status, shutdown and owned cleanup. All four daemon live scenarios therefore have
fresh dedicated-account evidence; no unrelated live engine aggregate was rerun.
Full pinned offline/race/lint/build checks and both macOS arm64 cross-builds pass.

Final review-fix acceptance passed in 161.388s: remote 74.74s (independent
download 12.70s), periodic upload 43.13s, watcher upload 43.51s (independent
upload 7.96s). This exercises the corrected post-write observation boundary and
cleanup cancellation ordering. A deterministic remote-only/combined regression
reproduced queued engine entry after failed remote cleanup, then passed with
synchronous lifetime cancellation while the shared gate is held.


## Linux desktop tray acceptance

Spec/plan 000014 implements the separate Linux `grove-tray` companion. Offline
protocol tests require `dbus-daemon`; they create private buses and test actual
D-Bus replies/signals, watcher replacement, root/menu layouts, disabled actions,
configuration matching, asynchronous service jobs, and coordinator shutdown.
Actual-binary tests use disposable populated profiles with absent synthetic token
paths and a fake-provider daemon helper; production gains no endpoint override.

```sh
go test ./internal/tray ./cmd/grove-tray
go test -race ./internal/tray ./cmd/grove-tray
```

On 2026-10-27 `TestTrayProcessLifecycle` passed in 0.39s and
`TestTrayProcessDaemon` in 2.34s. They verify stopped/waiting/syncing transitions,
duplicate rejection, watcher replacement, Refresh/Quit, SIGTERM, unchanged profile
state and preserved daemon ownership after tray exit. Required full offline/race
suites and pinned lint (0 issues) also pass; all three Linux binaries and both
existing Darwin arm64 cross-builds pass. No live Drive tests were enabled.

Actual desktop acceptance used Sway 1.12's headless/pixman backend, a private
runtime directory/session bus, Waybar 0.15.0 with only its tray module, a
disposable profile and the fake-provider daemon helper. A virtual pointer in
that isolated compositor exercised hover and right-click. Screenshots were
inspected for the stopped icon, plain status tooltip, rendered menu, waiting and
syncing icons, and re-registration after Waybar restart. The named icons were
not installed, exercising embedded pixmap fallback. Quitting the tray left the
daemon running; the helper stopped gracefully afterward. A real Waybar root-menu
property warning was reproduced by a regression and fixed before the fresh
warning-free run. The ordinary compositor/bar and its configuration were untouched.

The production systemd adapter was separately exercised against the actual user
manager through its normal session bus, without publishing an item to the normal
panel. A unique `groved@tray-acceptance-…` runtime unit directly launched a
fake-provider harness named `groved` with a private matching config. Observe
validated the effective command/config; explicit Start reached active lease
ownership; explicit Stop reached inactive ownership. The unique unit/config,
fixture and runtime artifacts were removed; no service was enabled and lingering
was unchanged. The 15s timeout on that disposable harness was a test cleanup
bound, not a tray-imposed daemon stop deadline.

To repeat desktop acceptance, use an isolated compositor/session bus and
explicit disposable profile, enable only the panel's tray module, launch the
actual companion, and exercise tooltip/menu, record/lease transitions, bar
restart and tray quit. Use only fixture-owned state. For manager acceptance,
reserve a unique runtime instance and verify/remove only its unit and artifacts.
Do not rewrite normal services or session configuration as a verification step.

No independent second SNI host is installed in this environment. Protocol tests
are not a substitute for desktop acceptance: broader host compatibility remains
unverified. The next gate is the same icon/menu/tooltip/restart/quit checklist in
an independent host such as KDE Plasma or another panel. Desktop-entry autostart
has static validation and documented quoting; actual login/autostart is untested.


Independent whole-change review at `ac1ec50` produced four Important findings;
`TestTrayStartupBusyHistory`, `TestServiceDelayedFailureResult`,
`TestTrayStartupControlCancellation`, and `TestServiceStopUnavailableProfile`
all failed before their production fixes, then passed. A job disappearing before
its completion signal now shows an explicit unknown outcome and retains its job
identity so a delayed failure remains visible. A further fresh stable observation
can restore control eligibility without clearing that historical warning.
Fresh formatting/lint/full offline/full race/build/module checks pass after the
fixes. The actual user-manager adapter acceptance also passed again afterward.
The rebuilt final binary also passed the isolated pointer-driven Waybar checklist
again after review fixes, with no menu/property warnings; its owned temporary
profile/session artifacts were removed.

## macOS desktop integration

Spec 000013's daemon, LaunchAgent controller, shared tray coordinator and native
menu-bar adapter are implemented on `design/macos-desktop-integration`. Linux
formatting, lint, full offline tests, race tests, and build pass. The macOS-15 CI
job runs native format/lint/offline/race/build checks, app bundle construction,
plist lint, Darwin subprocess lifecycle tests, and Linux amd64 CLI/daemon
cross-builds. The first hosted macOS lint run exposed Darwin-only issues; those
findings are fixed, and a fresh native run is pending. Interactive desktop
appearance and login acceptance are also pending. Do not describe AppKit
appearance, actual launchd lifecycle, or login behavior as verified until those
checks complete. Live Drive acceptance was not run.
