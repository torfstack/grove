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
