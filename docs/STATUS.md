# Project status

Updated: 2026-10-27.

## macOS authenticated daemon acceptance

2026-10-28: PR #23 merged into main at `e326049` after final Linux CI,
CodeQL and dependency review passed. On branch `test/macos-daemon-acceptance`,
the existing periodic daemon live tests are enabled on macOS. The disabled
gate passes with both tests skipped. After dedicated-account reauthentication,
the actual-binary periodic download and upload tests passed in 82.94s and
52.93s respectively. Both independently verified content, no-op cycles, offline
status, graceful shutdown and owned fixture cleanup.

An earlier run failed during fixture seeding due to `invalid_grant`. Its two
private records under `/private/tmp/grove-macos-daemon-acceptance-20261028`
retain pending initial root creations without recorded remote IDs. After
reauthentication, cleanup searches found no matching fixture objects and
conservatively refused to resolve either pending creation. These records remain
for future reconciliation; no deletion was inferred from the empty listing.

PR #24 is open. CodeRabbit's initial manual review request on `44660fe` was
rate-limited on 2026-10-28 at approximately 11:15 UTC, with the next included
review reported available in 29 minutes. No completed review or findings have
been received. Retry the initial review after the allowance resets, inspect its
summary, inline comments and threads, address valid findings, and wait for
fresh CI before the authorized merge.

## macOS daemon work

2026-10-28: PR #21 merged as `357f239`; final Linux and security checks passed. The user approved the macOS foreground daemon in spec 000013 and requested a branch. Implementation is on `feat/macos-daemon`; the paired implementation plan is `implementation-plans/000013-macos-daemon-plan.md`. Secure Unix configuration opening and signal handling now include macOS, and unsupported local watching is rejected before startup. Native process and cross-process lease tests passed, including fake-provider remote polling. Formatting, lint, full native offline and race suites, native build and final Linux cross-build passed. An independent review found no runtime defects; FIFO and early rejection coverage and stale roadmap wording were addressed. Foreign-owner rejection was not exercised with another account, and no live Drive test ran. A pre-existing status test fixture used noncanonical macOS temporary paths and was corrected. PR #23's initial CodeRabbit review completed on `0cb752d`, with two valid documentation findings, both addressed by clarifying macOS periodic sync and Linux-only watching. Linux CI and security checks passed on the reviewed head; fresh checks for the documentation fix are pending. No second review is requested.

## Linux desktop tray implementation

The user approved spec/plan 000014 and native execution. `grove-tray` is
implemented on `feat/linux-tray` in the managed worktree, with offline status,
StatusNotifierItem/D-BusMenu, distinct icon states, and optional validated
systemd user-service controls. All six slices and the independent
whole-change review are complete. The review of `ac1ec50` found four Important
issues, with no Critical or Minor issues. All four were reproduced by failing
regressions and fixed: busy startup with history cannot advertise normal health
before profile validation; delayed service job results retain correlation;
requested startup cancellation stays graceful; and Stop remains available for a
validated active service even if profile inspection is unavailable. Fresh full
offline/race/lint/build checks pass after those fixes. No rereview was requested.

Pinned formatting, lint (0 issues), full offline/race suites (13 packages),
module verification, all three Linux builds and both existing Darwin arm64
cross-builds pass. Local checks used the exact pinned task equivalents to avoid
the recorded host mise cleanup issue. Actual-binary lifecycle/fake-provider
daemon tests pass. Isolated Sway 1.12/Waybar 0.15.0 acceptance visually verified
icon, tooltip, menu, state transitions, watcher restart and tray-only quit. A
root-menu-property warning found by the host was reproduced and fixed before
the fresh warning-free run. The actual systemd adapter also validated/started/
stopped a unique disposable runtime service, observing active/inactive leases.
No service was enabled; lingering and ordinary desktop config were unchanged.
Owned service/desktop fixture artifacts were cleaned; no live Drive access ran.

A second independent tray host is unavailable, so broader tested compatibility
is not claimed. Login/autostart remains untested; the desktop-entry template
validates statically. Repeatable gates and limits are in
[TESTBED](TESTBED.md#linux-desktop-tray-acceptance). The macOS design remains a
separate unapproved proposal. [PR #21](https://github.com/torfstack/grove/pull/21)
is open against `main`.
Final-head CI exposed a timing race in the SIGTERM process test: it could signal
after item registration but before the initial status publication. The test now
waits for the published state before sending SIGTERM, and the event loop exits
cleanly when cancellation races with queued observations. The lifecycle test
passed 30 repeated runs; full lint, formatting, offline and race suites passed
locally. Fresh hosted checks are pending for the fix. Linux CI, CodeQL
Go/Actions and dependency review passed on reviewed publication head `4b0bcf9`.
Automatic CodeRabbit review was skipped because the repository
has fewer than ten stars; the manually requested initial full review completed
on `7499f6c..4b0bcf9`, processed all 36 eligible text files, and reported no
actionable findings or concrete merge-blocking issue. Its full summary and
walkthrough were retrieved and assessed; submitted-review records, inline
comments and GraphQL review threads are all empty. The 20 icon files and go.sum
were filtered by the reviewer; local icon/runtime and Go module checks cover
those artifacts. No second review was requested. Final documentation-head CI is
tracked in the PR checks; no feature merge has been requested.

## macOS daemon design

The approved slice is a foreground periodic `groved` runtime on macOS,
without local filesystem watching or launchd integration. The design is in
[spec 000013](spec/000013-macos-daemon-design.md); implementation is in progress
on `feat/macos-daemon` as described above.

## Authenticated systemd acceptance

`TestDaemonSystemdDownloadLive` passed three times on 2026-10-25 (67.74s,
67.51s, and 67.02s), then passed three times on the PR branch on 2026-10-26
(67.77s, 68.06s, and 71.14s), using the dedicated test account and the actual
user systemd manager. Each run
used a unique `groved@` instance and disposable profile, downloaded an owned
remote fixture mutation verified independently, completed a no-op cycle,
reported active/inactive ownership through offline status, stopped gracefully,
and cleaned fixture-owned remote data. The test removed its unique config,
drop-in, and temporary template when created; private run artifacts were also
removed. No service was enabled and lingering was unchanged. Logout/boot behavior
remains untested. Details and the repeatable gate are in
[TESTBED](TESTBED.md#systemd-user-service-acceptance).

The new gated acceptance test passes its explicit live run. Pinned `gofmt`,
`golangci-lint` (0 issues), and the full offline Go suite pass with the required
filesystem/loopback access. Initial sandboxed suite execution was blocked by
loopback and private-path restrictions; the approved unsandboxed rerun passed.
CodeRabbit's completed review found the test inherited an infinite stop timeout;
the disposable unit now sets 15s and the test asserts systemd loaded that bound.
Its sole finding was fixed in commit `cb2e155`; no second review was requested.
[PR #20](https://github.com/torfstack/grove/pull/20) merged into `main` at
`7499f6c` on 2026-10-26 after Linux CI, CodeQL Go/Actions, and dependency review
passed on final head `1d14bea`.

## Remote change detection implementation

[Spec 000012](spec/000012-remote-change-detection-design.md) and its
[paired plan](implementation-plans/000012-remote-change-detection-plan.md) are
implemented on `feat/remote-change-detection` in `/tmp/grove-live-watch-acceptance`.
The user authorized the direction and delegated routine choices; decision 0012
records opt-in intervals, account-wide hints, ephemeral cursors, bounded checks
and serialized API work. Tasks 1–5 pass their normal/race checks and real-process
acceptance. No engine mutation rules or dependencies changed.

Explicitly enabled dedicated-account acceptance passed in 115.697s on 2026-10-25:
remote changes 73.73s (independent download verification in 12.40s), local watching
41.96s (independent upload verification in 7.30s). Both used actual binaries,
ten-minute periodic intervals, settled no-op checks, offline status, SIGTERM,
and fixture-owned cleanup. Remote live acceptance enables local watching for
explicit later no-op requests; remote-only behavior is separately verified with
fake-provider real processes. No credentials, cursors, generated IDs or synced
contents were printed. Periodic live regressions also passed in 110.647s (download 68.34s, upload
42.30s), each with independent verification, no-op checks, shutdown and owned
cleanup. Final formatting, lint (0 issues), all eleven offline/race packages,
both Linux builds and macOS arm64 cross-builds pass. Relative documentation links
and diff checks pass. PR #19 merged into `main` at `f6d23b5` on 2026-10-25;
final Linux CI, CodeQL Go/Actions, and dependency review passed on the merged
head. The primary checkout is synchronized with `origin/main`.

## Remote detection review and final acceptance

CodeRabbit completed its initial review of PR #19 at `75892e3`; its summary,
inline comment and review thread were retrieved. Its sole finding was valid:
the current-status paragraph still claimed no implementation. That sentence is
corrected; the sequential implementation history remains intact.

Independent review found a cleanup race: releasing the remote-check gate before
publishing a fatal cleanup error could let queued engine work begin. The daemon
now cancels its shared lifetime while still holding the gate. A deterministic
regression reproduced the failure for remote-only and combined sources before
the fix. Native reconciliation also skips an already canceled lifetime, preserving
the original fault. A minor live-harness correction captures the post-write
attempt boundary after WriteFile succeeds, retaining the earlier latency clock.

Fresh actual-binary acceptance after these fixes passed in 161.388s: remote
74.74s (independent download 12.70s), periodic upload 43.13s, and watcher upload
43.51s (independent upload 7.96s). All verified content, no-op stability, offline
status, shutdown and fixture-owned cleanup. Earlier periodic-download acceptance
remains valid. Final pinned formatting, lint (0 issues), all eleven offline/race packages,
Linux builds and both macOS arm64 cross-builds pass after the fixes. CodeRabbit's
completed review found one valid status-wording issue, which was fixed; no
second review was requested. PR #19 merged into `main` at `f6d23b5` after all
final-head checks passed. Non-Linux runtime, network filesystems and
authenticated systemd boot/logout remain outside this acceptance.

## Live watcher acceptance and remote detection

The user explicitly enabled dedicated-account live watcher acceptance. The first
run exposed a harness observation race: an immediate no-op follow-up can replace
the upload's historical counters before the test observes waiting status. Retained
baseline data proved the expected edit committed, with no pending transaction;
all nine owned fixture objects were cleaned. The test now waits for a completed
attempt started after the edit, then independently verifies Drive bytes rather
than relying on transient counts. No production behavior changed.

The fresh actual-binary rerun passed in 42.888s; upload content was independently
verified in 7.50s against a ten-minute periodic interval. Subsequent no-op cycles
preserved file hashes/mtimes, offline status and SIGTERM shutdown passed, and
fixture-owned cleanup completed for both runs. Tokens and generated Drive IDs
were not printed. The user authorized remote change detection as the next slice;
[spec 000012](spec/000012-remote-change-detection-design.md) and
[paired plan](implementation-plans/000012-remote-change-detection-plan.md) are
prepared under the standing autonomous workflow. Agent choices are opt-in
remote_poll_interval (minimum 10s), account-wide My Drive hints, ephemeral cursor,
10s bounded checks serialized with engine work, historical check status and the
unchanged full-scan safety net. The builder check found no consequential gaps.
Native implementation followed six tasks; the sections above record the implemented behavior and acceptance results.

## Dependency and record refresh

[PR #18](https://github.com/torfstack/grove/pull/18) refreshes compute metadata
v0.3.0 → v0.10.0, pflag v1.0.9 → v1.0.10, mise-action 5.2.1 → 5.2.3, and
CodeQL action 4.39.0 → 4.39.1. Action pins are immutable and aligned. Metadata's
Go 1.26 and x/sys v0.46 minimums remain below Grove's existing pins. Direct Go
dependencies and pinned Go/lint/gh tools were current; no open Dependabot alerts
or dependency PRs were reported at audit time. Unused module-graph entries were
not independently upgraded. No authentication, profile, or sync behavior changes
are intended.

Module verification, formatting, lint (0 issues), full offline/race tests, both
Linux builds, macOS arm64 cross-builds, and documentation/diff checks pass.
Pinned task-equivalent commands ran directly to avoid unrelated host mise
cleanup; hosted CI runs the existing mise tasks. All hosted Linux/security checks
passed on `474e914`. The independent read-only review found no issues.
CodeRabbit's completed full review of `b6f2ad0..474e914` selected all nine
reviewable files and reported minimal merge risk with no actionable findings.
Its complete summary, submitted reviews, inline comments, and GraphQL threads
were retrieved; the latter three are empty. go.sum was filtered by CodeRabbit
and is verified by Go tooling. No second review is requested under decision
0009. PR #18 tracks final documentation-head checks and authorized integration.

Current product/architecture/roadmap and spec statuses now distinguish merged
multi-candidate conflicts (PR #9), deletion propagation (PR #10), and watching
(PR #17) from genuinely deferred work. Historical session narratives below are
retained. No new requirements or product decisions were introduced.

Environment note: an initial mise invocation unexpectedly removed the older
host-global Codex 0.163.0 installation. Subsequent work uses pinned binaries
directly to avoid unrelated host cleanup; no project tool pins changed.

## Systemd user integration

PR #16 is now merged into main at `23c583b` (2026-10-24); GitHub merge
state was confirmed and origin/main refreshed. The implementation/review
narrative below is historical and its no-merge-authorized wording is superseded
by the user's completed merge.

## Local watching

[Spec 000011](spec/000011-local-watching-design.md) and its
[implementation plan](implementation-plans/000011-local-watching-plan.md) are
approved and implemented. Opt-in `watch_local` defaults false; Linux native
hints request debounced sequential shared-engine attempts while periodic scans
continue. Configuration/status, scheduling, recursive coverage, lifecycle,
process acceptance, and gated live harness/operator docs are complete.

All local fmt/lint/offline/race/build checks pass, including the dependency-
integrated tree and regression-backed shutdown/attempt-classification fixes.
Both binaries cross-build for macOS arm64. The independent review's two
Important findings are fixed; CodeRabbit's completed initial review of `9fb0c84`
selected all 31 files and found no actionable issues, comments, or threads.
[PR #17](https://github.com/torfstack/grove/pull/17) merged into main at
`b6f2ad0` on 2026-10-24 after all final-head CI/security checks passed. The user authorized publication, one completed CodeRabbit review,
fixes, passing CI, and merge, followed by continued bounded work.

Authenticated local watching passed on 2026-10-25: the actual-binary test
completed in 42.888s, independently verified the watch-triggered upload in 7.50s,
checked no-op content/mtime stability, offline status, SIGTERM, and owned cleanup. Native descriptor churn is
covered without forcibly injecting kernel descriptor reuse. Network filesystem
reliability and non-Linux daemon runtime are not claimed. Native registration
requires `/proc/self/fd`; fatal watch/root errors stop visibly.

Dependency maintenance is complete: PR #11 merged at `002a20e` and PR #14 at
`750ba2a`, each after its completed review and fresh passing checks. The primary
checkout is on main; original design-only drafts are preserved in a named stash.
Detailed implementation, review, verification, and integration history follows.

## Systemd implementation and verification history

PR #15 is merged into main at `63de937`; its review and verification history
below is historical. The user approved spec and plan 000010 for native execution.
The template and guide are implemented on `feat/systemd-user-service`:
[operator guide](../contrib/systemd/README.md),
[spec](spec/000010-systemd-user-service-design.md),
[plan](implementation-plans/000010-systemd-user-service-plan.md), and
[decision 0010](decisions/0010-systemd-user-service.md).

Full offline tests and both native builds pass. Systemd 262.1 validates the
copied template plus executable override without diagnostics; the unmodified
vendor command reports the expected missing `/usr/bin/groved` in this source
checkout. Runtime-only helper units verified path quoting, literal escaped
instances, journal output, effective lifecycle settings, delayed graceful stop,
and no automatic restart after failure. Actual groved rejected a disposable
invalid config with a sanitized journal error and stayed failed without token
or network access. The exact guide setup refused a second installation without
changing existing files; real-daemon signal/exclusion process tests also pass.
All acceptance units/state were removed, no service was enabled, and lingering
was untouched. See [TESTBED](TESTBED.md#systemd-user-service-acceptance) for details.

Initial sandbox test attempts failed because cache writes, filesystem inspection,
and loopback listeners were blocked. Successful verification used a temporary
Go cache and approved escalation. No Go code changed. Authenticated sync under
systemd and logout/boot/linger behavior were not tested. The independent
whole-change review found one Important issue: unit installation ignored custom
XDG_CONFIG_HOME. The guide now uses the effective user-unit directory while
retaining the approved fixed Grove JSON location. A reproduced failing guide
check passes after the fix for default/custom config homes with spaces, and
repeat setup preserves files. No Critical/Minor findings remain. Review accepted
the recorded synthetic-home, in-place-branch, and configuration-validation
choices. Live systemd sync, real boot/logout/linger, a separate account with
spaces in its home, distribution packaging, and non-Linux integration remain
explicit verification/scope limits. PR #16 publication and hosted-review progress
are recorded below; no merge is authorized.

## Release status

Grove is `pre-release`, with zero deployed users. Earlier-release compatibility
and migrations are outside the current scope. The durable switch is the
release-status line in [AGENTS.md](../AGENTS.md#release-compatibility): change it
to `live`, or tell the agent Grove is live, to require compatibility and migration
planning. Current-run data safety and interruption recovery remain required.

## Established

- Project name: Grove; language: Go (the user has more experience with Go).
- Repository: `git@github.com:torfstack/grove.git`; Go module:
  `github.com/torfstack/grove`.
- Linux first; Windows and macOS later.
- One-shot `grove` CLI and later `groved` daemon sharing the sync engine.
- Development tooling tracked in `mise.toml`.
- Repeatable Drive fixtures and opt-in live integration tests are agreed.
- `grove auth` is implemented with Cobra and golang.org/x/oauth2. It supports
  read-only or explicit read-write access, browser login with loopback callback,
  PKCE/state validation, cancellation, and atomic private token storage.
- Offline tests cover CLI validation, fake-provider OAuth, invalid and concurrent
  callbacks, cancellation, token persistence, and save failure preservation.
- Formatting, vet, offline tests, race tests, and builds are configured in mise.
- golangci-lint 2.14.0 is pinned in mise with an explicit five-linter rule set
  and gofmt checks. `mise run lint` checks production and test code.
- A minimal Linux GitHub Actions workflow runs lint/formatting, offline tests,
  race tests, and build on pull requests and pushes to main. GitHub CLI is pinned
  in mise for PR management.
- Dependabot alerts and security updates are enabled. Secret scanning and push
  protection were already enabled and were verified through the GitHub API.
- Dependabot weekly version updates for Go and Actions, CodeQL for Go and Actions,
  and high/critical dependency review are prepared in PR #1. Version-update
  schedules become active after merging the configuration to main.
- CodeRabbit advisory review configuration is prepared. The user has no paid
  Copilot plan; use CodeRabbit's free reviews for this public repository instead.
  Its GitHub app is installed; an initial skipped-review notice showed the
  repository configuration was loaded. Main is explicitly enabled for reviews.

## Completed initial download sync

Fixture tooling and initial download sync are implemented together on
`feat/initial-sync`, following approved [spec 000002](spec/000002-initial-sync-design.md)
and its [plan](implementation-plans/000002-initial-sync-plan.md). Native execution
completed private records/locks, persistent OAuth refresh, Drive HTTP access,
fixture lifecycle, deterministic planning, rooted downloads and recovery, CLI
wiring, and the opt-in live suite. Offline and race tests, lint, formatting, native
build, and Linux cross-build passed. Dedicated-account live acceptance passed on
macOS on 2026-10-10: seed, inspect, sync, independent verification, no-op repeat,
and fixture-owned remote cleanup. Token contents were not printed. Whole-branch independent
review found three valid issues (filesystem-alias exclusion, refresh redirects,
and fixture root ordering); regression tests reproduced them and all are fixed.
[PR #5](https://github.com/torfstack/grove/pull/5) is merged into main at `2805575`. Linux CI and security
checks passed on the original implementation. CodeRabbit's three findings are
fixed: fixture cleanup now processes the root last, destination preflight checks
hardlink publication support, and the auth exclusion test reaches the token lock.
Formatting, lint, offline tests, and race tests passed after these fixes. Latest
main (Dependabot PRs #2–#4) is merged into the branch. Linux CI, dependency review,
CodeQL, and CodeRabbit passed on `29e775e`. CodeRabbit's latest review generated no
actionable comments and marked the three original threads resolved. Live
acceptance passed with the user; PR #5 is merged.

The user previously placed Desktop OAuth credentials at ~/google_client_secret.json.
Dedicated test-account browser authentication succeeded. The initial auth
implementation and Linux CI from
[PR #1](https://github.com/torfstack/grove/pull/1) are merged into main.
Requirements and design specs live in `docs/spec/`. Implementation plans live in
`docs/implementation-plans/` and reuse their corresponding spec's six-digit number.
Authentication and sync token sessions now share advisory locking. Token writes
remain atomic, and concurrent writers for a token or sync profile are refused.

## Incremental download sync

Approved spec 000003 and its plan are implemented and merged into main through
[PR #6](https://github.com/torfstack/grove/pull/6) at `69768d6`. The engine now
plans remote additions, content updates, metadata-only changes, and safe file/
folder moves. Persisted local hashes detect conflicts before new operations.
Version-1 profiles recover old pending work and atomically migrate to version 2.
Transfers, recovery, baseline persistence, replacements, and subtree moves have
focused boundaries within the existing engine. No second engine or generic
workflow framework was added.

Offline tests cover real HTTP fixture mutation → inspect → incremental sync →
independent verification → no-op → owned cleanup; journal-save interruption,
malformed records, local conflicts, remote changes during download, and filesystem
case aliases also have regression coverage. Independent review found temporary/
probe ownership gaps, a pending-path collision, and superficial migration tests.
These are fixed with persisted artifact identities, verified-stage hash checks,
collision validation, and actual version-1 recovery/save-failure tests.
Formatting, lint (0 issues), full offline/race tests, native macOS build, Linux
amd64 cross-build, and diff checks pass after the review fixes. Dedicated-account
live acceptance passed on macOS on 2026-10-10 using the explicitly authorized
test token: incremental sync in 83.43 seconds, initial sync in 41.06 seconds,
and the complete live suite in 124.938 seconds. Both workflows verified content,
no-op repeats, and fixture-owned remote cleanup. Token contents and generated
Drive IDs were not printed. Linux live integration remains untested.
Linux CI passed lint, offline tests, race
tests, and the CLI build on `d30b2bf`; dependency review and CodeQL also passed.
CodeRabbit's four findings are fixed: stale untouched intents, direct x/sys
dependency classification, and two documentation inconsistencies. Its completed
follow-up review of `d30b2bf` generated no actionable comments and reports the
earlier concerns resolved. Final documentation commit `21581e1` also passed Linux
CI, dependency review, CodeQL, and CodeRabbit, with no new actionable comments.
Live-acceptance documentation commit `c5f12a3` passed Linux CI, dependency review,
CodeQL, and CodeRabbit with no new actionable comments. One original move-recovery
thread still shows open despite that summary; its requested changes are present
and verified. No replies or
manual thread-resolution actions were posted.

Known recovery limitation: a crash after exclusive artifact creation but before
its ownership record is durable preserves the artifact and stops recovery for
manual inspection. CodeRabbit retained this nonblocking reliability concern,
consistent with the accepted ownership-safety boundary. Recovery controls in
spec 000005 handle recorded upload transactions; reconciliation of unrecorded
artifacts remains deferred.

## Approved local upload sync

Implementation of approved [spec 000004](spec/000004-local-upload-design.md) is
complete for the current scope on `docs/local-upload-design`, following its
paired plan. Grove now has an explicit `grove sync --two-way` path; the normal
path remains read-only. The shared engine uploads local edits when Drive's head revision and modification time
match the saved baseline, persists the new revision as the next baseline, and
uploads a later local edit on a subsequent run. It also has durable `ask` conflict
candidates, profile policy/list/resolve service operations, and CLI controls for
`ask`, `keep-local`, and `keep-cloud`.

The engine detects remote changes before upload, captures a single post-preflight
competing revision into a durable conflict, and supports `ask`, `keep-local`, and
`keep-cloud` through profile service methods and CLI commands. The opt-in live
acceptance passed on macOS: an ordinary upload, a later local edit, a remote
write injected during upload, independent verification of the preserved revision,
and cloud-side resolution back to Drive. The focused live test used the dedicated
test token and fixture-owned remote data; cleanup completed. The broader
`mise run test-live` passed incremental sync, current-head
pinning, conditional-upload probing, and upload/race recovery, but the initial
sync case hit a transient fixture transport error during cleanup. A standalone
retry then failed while seeding; its partial fixture was cleaned successfully.

Live testing also fixed the initial empty `pageToken` that Drive rejected with
HTTP 403 `pageTokenExpired`. Drive rejects unpinning an old revision after it
becomes non-head, so successful uploads and captured conflict revisions retain
their keepForever pins. The 200-pinned-revision per-file quota is therefore a
lifetime limit; Grove fails closed when full. Pinning can increment Drive's
metadata version without changing its content revision, so version-only increases
are allowed when head revision, modifiedTime, size, MD5, identity, and path still
match. Multiple simultaneous post-preflight candidates remain pinned and
journaled, but are not yet exposed for interactive resolution. Linux live runtime
acceptance remains untested; Linux amd64 cross-build passed.

Final local verification on 2026-10-10: `mise run fmt`, `git diff --check`,
`mise run lint`, `mise run test`, `mise run test-race`, native build, and Linux
amd64 cross-build passed. The full live-suite limitation above remains open.

Live suite rerun on macOS on 2026-10-12 using the dedicated token and a fresh
private run directory: incremental sync, current-head revision pinning, the
conditional-write probe, and two-way upload/concurrent-revision recovery passed.
Initial sync failed because Drive reported a changed metadata version while
identity, parents, content hash/size, ownership, and trashed status still
matched. The conditional-write probe confirmed Drive accepted a stale version
condition with HTTP 200; it restored the competing content and cleaned its
fixture. The failed initial-sync fixture was cleaned with the ownership-checked
CLI. Token contents and generated Drive IDs were not printed. Initial-sync live
verification remains unresolved; Linux live runtime remains untested.

The preexisting status correction recording PR #6's merge was committed as
`9ba3d41` on the design branch.

[PR #7](https://github.com/torfstack/grove/pull/7) is merged into main at `f58620d`. Linux CI,
dependency review, and CodeQL passed on `2bd237d`. CodeRabbit reviewed that commit
and posted seven findings. Six are addressed in the current follow-up: correct
HTTP status reporting, upload-specific version-only recovery, pending unsupported
local choices for independent additions, upload counts, cloud-resolution baseline
timestamps, and preservation of pre-existing revision pins. Journals without
recorded pin ownership preserve the pin conservatively.

The suggestion to discard an uncertain upload/create intent after observing an
old head or a missing allocated ID is declined: neither observation proves a
potentially in-flight request has settled. Discarding the identity could authorize
a blind update or a duplicate create. Regression tests verify retained journals
and stages for those observations. Operator reconciliation remains follow-up
work; such profiles can remain blocked when no conclusive result appears.
Follow-up verification on 2026-10-11 passed formatting, diff checks, lint,
offline tests, race tests, native build, and Linux amd64 cross-build. The live
initial, incremental, pinning, and upload/conflict-recovery scenarios passed.
The aggregate suite failed in the conditional-write probe's metadata read; its
standalone retry reached cleanup and failed with a transport error. Retained
fixtures were cleaned successfully with the ownership-checked CLI. Hosted checks
passed on `c76dcb5`; its review raised the legacy-compatibility finding addressed
in the review scope update below.

## Verification

Dependabot PR #2 now pins CodeQL `init` and `analyze` to the same revision,
`24c54180a607b1449ed407dd24f251e4e9147c8d`, addressing CodeRabbit's finding
about unsupported mixed versions. The workflow-only fix preserves existing
inputs, permissions, and Go tooling. Fresh hosted CI and CodeRabbit review are
pending after pushing the fix; inspect both before reporting readiness.

Agent PR workflow now requires waiting for CI and CodeRabbit, retrieving feedback,
and addressing valid findings before reporting readiness. CodeRabbit's docstring
coverage quota and generation suggestion are disabled to match our sparse-comment
policy; useful documentation feedback remains welcome.

Formatting, go vet, offline tests, and race tests passed. Native macOS and Linux
amd64 builds passed. The hosted CI workflow runs the offline suite on Linux;
see PR #1 for its current result. Real Linux browser integration is still untested.
A fresh code review found a callback response shutdown race; a regression test
reproduced EOF before the fix and passed afterward with race detection. Live
Google consent and the baseline Drive workflow are now verified on macOS;
Linux browser and live Drive integration remain untested.

## Repository housekeeping

Expanded `.gitignore` to cover Go build and test output, editor and local agent
state, OS metadata, and environment files while preserving existing project
credential and binary rules. Go source, module files, mise configuration, and
environment examples remain trackable. Verified with `git check-ignore` and
`git diff --check`; no Go code changed.

## Initial sync verification

Offline CLI HTTP integration demonstrates seed → inspect → sync → independent
verify → no-op repeat → cleanup with page size 2. Recovery tests inject journal
write failures at intent, transfer, verified-hash, and completion boundaries;
checksum/version changes, cancellation, and racing destinations fail safely.
Process tests exercise token/profile/destination exclusion on macOS; Linux runtime
coverage will run through CI. The Linux cross-build is compilation evidence only.
The live acceptance command and dedicated-token setup are documented in README.

The enabled live suite passed in 40.70 seconds using a dedicated local token.
Home contains a `.git/hooks` directory used by global `core.hooksPath`, so the
conservative outside-Git guard rejected the documented home paths. The successful
run used private temporary paths outside that marker. Its remote fixture objects
were trashed; local records remain private. An earlier sandboxed attempt failed
at the first create. Authenticated inspection found no matching remote object;
its pending record is retained because an absent listing does not resolve an
uncertain create. Token contents and generated remote IDs are not recorded here.

## Review scope update

The user confirmed that Grove has no deployed users. Removed the legacy-profile
compatibility changes from `b7f9730`; CodeRabbit's legacy revision-metadata finding
is declined as outside the agreed pre-release scope. Current-profile metadata
checks remain strict. The release switch and post-launch migration requirements
are recorded in AGENTS.md. Formatting, lint, offline tests, race tests, and diff
checks passed after removing the compatibility changes. Hosted CI and CodeRabbit
completed on `9226afc`. The latest review found a metadata-only change could
produce a duplicate ask-policy conflict on retry. A regression reproduced the
changed identity; conflict identity now excludes metadata version while retaining
content revision and fingerprints. A different revision still receives a distinct
identity. Formatting, lint, offline tests, race tests, and diff checks pass for this fix.
Hosted checks passed on `a541fe3`; CodeQL's first attempt hit an unauthenticated
GitHub API rate limit during mise tool setup, and the failed-job retry passed.
CodeRabbit's recent-review summary explicitly covers `9226afc` → `a541fe3` and
reports no actionable comments. It then automatically paused future reviews after
several reviewed commits. This documentation-only follow-up may therefore be
skipped; resuming future bot reviews requires an authorized GitHub bot command.

The earlier regressed-version suggestion is declined: uploads perform a fresh
Get and compare revision, timestamp, identity, and content before staging; matching
content with a different metadata version is deliberately allowed. A version
regression alone does not establish a content conflict. The uncertain-write
abandonment suggestion remains declined for the data-safety reasons above.
These two review threads remain open; no replies or manual resolutions were
posted. The same-path policy thread is now automatically resolved by CodeRabbit.

## Operator reconciliation

The user selected operator recovery after merging PR #7 and agreed a focused
`recovery inspect`, `recovery retry`, and `recovery export` scope. Approved
[spec 000005](spec/000005-operator-reconciliation-design.md) was approved by the
user on 2026-10-11. Retry processes only the pending transaction; export copies
verified staged bytes without abandoning uncertain intent. Force-abandon and
multiple-competitor selection remain deferred. Inconclusive requests can still
block indefinitely; this slice does not promise safe reset.

The user selected native execution of the
[paired plan](implementation-plans/000005-operator-reconciliation-plan.md).
Implementation is on `feat/operator-reconciliation`: offline structured diagnosis,
recovery-only retry, verified private stage exports, and CLI controls with
sanitized output. Retry verifies remote structure and independently downloaded
bytes before recording outcomes, reassesses anchored history without discarding
competitor obligations, and retains a durable retired journal through pre-send
cleanup. Export uses rooted copies and no-replace publication under profile,
directory and registry exclusion. It leaves the journal and source stage intact.

Regression tests exposed and fixed create-journal validation rejecting an empty
previous baseline and a verified result, plus safe error wrapping discarding
joined resource-close failures. Coverage includes unknown outcomes, partial and
changed stages, save interruptions, policy-independent conflict capture,
publication failures, source/parent replacement, output collisions, and concurrent
registration/profile exclusion.

Local verification on 2026-10-12 passed `mise run fmt`, diff checks,
`mise run lint` (0 issues), full offline tests, full race tests, native macOS build,
and Linux amd64 cross-build. The initial sandboxed baseline failed because cache
writes and local HTTP listeners were denied; the permitted retry passed.
No live tests ran. Linux live runtime acceptance remains untested. Independent
whole-branch review of `e860420` found five important issues; all five were fixed
in `6d89055` and covered by regression tests. The focused recovery tests, full
offline suite, full race suite, lint, native build and Linux cross-build passed
after those fixes. No additional independent review was run. The branch is
pushed to `origin` and PR #8 is open against `main`. CI passed on `c5dd8c6`.
CodeRabbit's completed review identified a possible export temporary-path
substitution immediately before publication. A regression test reproduced
publication of substituted bytes. Export now stages in a private directory and
links from its open directory handle. A follow-up review found that failures
opening the staging root could leave its empty directory; identity-checked
cleanup is now registered before opening it, and unsupported platforms stop
before staging. All required local checks passed after these fixes. If the
directory identity itself cannot be read, cleanup avoids removing an unverified
path; this rare error can leave an empty private directory.
CodeRabbit then found that a directory-handle close failure after successful
publication could be reported as if no output existed. Publication success is
now tracked separately from close errors, with a regression that verifies the
exported bytes and published flag. Formatting, lint, full offline tests, race
tests, native build and Linux cross-build passed. Fresh CI, CodeQL, dependency
review, and CodeRabbit all passed on `5ff663b`. CodeRabbit's review covered
`98009ef` through `5ff663b`, reported no actionable comments, and resolved the
temporary directory thread. On head `d118fdc`, CI, CodeQL, dependency review,
and the CodeRabbit status check passed. The user authorized a manual
`@coderabbitai review` request. CodeRabbit reported that the review finished,
but its incremental system does not re-review already reviewed commits; the
documentation-only commits were skipped as similar to previous changes. No new
inline comments or review threads were posted, and the existing code findings
remain addressed. PR #8 merged into `main` at `24e2ebb`. The rare Lstat
ownership-unknown cleanup case is recorded above.

Multiple conflict candidates are being implemented on branch
`feat/multiple-conflict-candidates` from the approved [spec 000006](spec/000006-multiple-conflict-candidates-design.md)
and [implementation plan](implementation-plans/000006-multiple-conflict-candidates-plan.md).
Tasks 1–3 are committed as `3352fd3`, `6f7289e`, and `cfcbf49`; their focused
sync-engine suites passed. Task 4 is committed as `02e1611`; focused CLI and
sync-engine conflict/recovery tests passed. Full local verification passed on
macOS: `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`,
`mise exec -- go build ./...`, and `GOOS=linux GOARCH=amd64 mise exec -- go build ./...`.
The live Drive suite was not enabled. PR #9 targets `main`. Its first CodeRabbit
review found a recovery-order bug: a newly discovered earlier revision could be
journaled after a later candidate and make state unloadable. The regression test
reproduced the issue, and captures are now inserted in stable order before each
journal save. The regression test reproduced the issue before the fix and now
passes. Full local checks passed again after the fix. On head `0f2be53`, Linux
checks, CodeQL, dependency review, and CodeRabbit passed. CodeRabbit's recent
review reported no actionable comments; it skipped the three changed files as
similar to the earlier review. The original recovery-order thread is outdated
and marked resolved by GitHub after the fix. The live Drive suite remains
disabled; Linux live acceptance is still for the Linux machine.

## Local deletion propagation

After PR #9 merged, the user selected local deletion propagation as the next
feature and approved the design direction: on explicit two-way sync, trash a
matching Drive item only when it still matches Grove's saved baseline; preserve
remote changes and surface a conflict. The first slice covers files and folders.
Approved [spec 000007](spec/000007-deletion-propagation-design.md) and its paired
[implementation plan](implementation-plans/000007-deletion-propagation-plan.md)
are implemented on `feat/deletion-propagation`. The planner now supports
missing tracked files and topmost folder subtrees, producing either a journaled
Drive Trash operation or a deletion tombstone when the remote state changed.
Trash confirmation distinguishes an explicitly trashed root from descendants
inherited as trashed, and an uncertain request is recovered by reading the same
IDs without retrying the mutation. Explicit deletion conflict choices either
trash the reviewed subtree or restore its current contents through normal
verified downloads; configured conflict policy does not select a side.

Full local formatting, lint (0 issues), offline/race suites, native build, and
Linux amd64 cross-build pass. The enabled macOS live suite passed in 313.614
seconds, including initial/incremental sync, revision pinning, upload recovery,
and deletion. After review fixes, focused live deletion acceptance passed in
49.349 seconds with exactly two Trash requests, independent root/descendant
verification, no-op repeat, and fixture cleanup. A prior verification read failed
after Trash and a retry failed while seeding; both retained fixture runs were
cleaned with the ownership-checked CLI. Direct reads confirmed inherited Trash
flags and showed metadata versions increase while content revision/time/hash
remain stable.

Independent static review found nested-journal validation, conflict/journal
coexistence, post-Trash fingerprint verification, and partial cloud restoration
issues. Regression tests reproduced the blockers and all findings are addressed.
Cloud restoration keeps profiles incomplete until downloads finish, preserves
unrelated tombstones, permits their explicit resolution during partial restore,
and resumes through download-only sync. PR #10 is open. Its first CodeRabbit
review found that unrelated local edits could strand cloud restoration and that
conflict filtering could mutate a planned sibling through a shared slice. Both
findings are fixed with regression coverage in commit `f377e79`. The suggestion
to retire an uncertain Trash journal when a read shows the item active is
declined: an in-flight request can still settle after that read, so clearing the
journal would permit an unsafe blind retry. The original mutation error is
available for diagnostics while the returned message remains sanitized. On
`f377e79`, local formatting, lint, full tests, race tests, native build, Linux
amd64 cross-build, Linux checks, CodeQL, and dependency review passed. CodeRabbit
was still marked “Review in progress” at 09:14 UTC, about five minutes after it
started; no completed review for that head was available. Next, inspect the
CodeRabbit summary, inline comments, and threads for the latest PR head, assess
any new findings, and address valid ones. Linux live runtime results are recorded
below; cross-build is compilation only.
Drive Trash is reversible, but its API has no conditional write; the spec records
the residual race between final preflight and the request. Local file watching
remains planned with daemon orchestration; one-shot sync rescans local state on
each invocation.

## Linux live acceptance, 2026-10-22

On Linux x86_64 at `8123f12`, the explicitly enabled `mise run test-live`
finished in 270.050 seconds with one failure. Configuration validation, local
deletion, incremental sync, current-head revision pinning, conditional-write
probing, and two-way upload/concurrent revision recovery passed. Initial sync
rejected a metadata-version change during download while identity, parents,
content size/hash, ownership, and active status still matched. This repeats the
previously recorded macOS symptom. Independent fixture inspection verified five
files and three directories, and ownership-checked cleanup succeeded.

A fresh focused `TestInitialSyncLive` retry passed in 38.686 seconds, including
verification, no-op repeat, and owned cleanup. Every live scenario has now passed
on Linux, but the aggregate run was not green; the intermittent initial-download
version check remains unresolved. Next, investigate why Drive advances metadata
version after fixture seeding and whether the download consistency check can use
a proven content identity without weakening change detection. Private records
remain outside Git; credentials, generated IDs, and synced contents were not
printed. The initial sandboxed attempt could not create private run directories
and failed temporary-path validation before Drive operations; rerunning with
the required filesystem/network access passed configuration validation.

## Download metadata-version reliability

Approved [spec 000008](spec/000008-download-metadata-version-design.md),
[decision 0007](decisions/0007-download-metadata-version-proposal.md), and the
paired [plan](implementation-plans/000008-download-metadata-version-plan.md)
are implemented natively on `fix/download-metadata-version`. One comparison now
validates transfer, initial-download recovery, and unpublished replacement
recovery. Higher numeric versions require unchanged revision/time, size/hash,
and file structure/access. Missing revision/time evidence retains the strict
version check. Planned journal snapshots remain intact, and subsequent scans
reconcile newer metadata without another transfer or destination-file rewrite.
Move, upload, and Trash preconditions retain their existing rules.

Regressions reproduced the transfer and recovery failures before the respective
fixes. Deterministic tests cover byte and metadata rejection, disk-reloaded
interruption phases, save faults, artifact preservation, legacy strict fallback,
HTTP reconciliation, and cloud restoration with an unrelated deletion tombstone.
Existing stale-content fixtures now change revision as well as version, so they
continue testing real changes alongside the new version-only acceptance cases.

Formatting, lint (0 issues), full offline/race suites, and the native Linux build
pass. The explicitly enabled full Linux live suite passed in 279.377 seconds:
configuration, deletion (46.65s), incremental sync (73.86s), initial sync
(38.32s), revision pinning (23.44s), conditional-write probing (32.16s), and
upload/concurrent revision recovery (64.94s). All scenarios completed their
fixture-owned cleanup. No credentials, generated IDs, or synced contents were
printed or committed. This is a passing aggregate run, unlike the earlier
failed run and focused retry recorded above. The cause of that original Drive
version increase remains unknown; deterministic cases establish the exception's
safety rule without attributing a cause.

Independent read-only review of `8123f12..1967fc8` found no critical, important,
or minor issues and no material test gaps; nothing was declined as outside
scope. The reviewer confirmed plan alignment and the recorded execution
choices. The user authorized opening a PR and rerunning tests with the dedicated
local token. [PR #13](https://github.com/torfstack/grove/pull/13) targets `main`.
Fresh lint, uncached offline/race suites, and the native Linux build pass. A full
local-token live rerun passed in 275.451 seconds with fixture-owned cleanup.
CI, CodeQL, and dependency review passed on `cf301cd`. At 19:46 UTC,
CodeRabbit still reported "Review in progress" for that head, nearly ten minutes
after starting at 19:37 UTC. Its summary named the correct commit and selected
all 19 changed files, but the reviews API, inline-comments API, and GraphQL
review threads contained no feedback. CodeRabbit's
[status page](https://status.coderabbit.ai/) reported degraded GitHub pull-request
performance; that does not establish the cause of this particular delay.

CodeRabbit subsequently completed its implementation review of
`8123f12..cf301cd` and reported no actionable comments, minimal merge risk,
and no architecture-level concerns. The complete summary was read through
GitHub, and the inline-comments API and GraphQL review threads were checked:
both contained no findings. Its retained comparison and recovery-safety
observations agree with the approved scope; no fixes or declined suggestions
remain from that review.

Documentation-only follow-ups record the fresh local-token acceptance and
review outcome. No Go code changed after the reviewed implementation. The PR
remains open for the user's integration decision; check the latest CI and
CodeRabbit status before merging.

## Minimal Linux daemon design

[PR #13](https://github.com/torfstack/grove/pull/13) merged into `main` at
`ccfa67e` on 2026-10-22. Its final checks passed; CodeRabbit found no actionable
implementation findings and skipped re-reviewing the documentation-only changes
as similar. The earlier PR-open narrative above records that session's state.

The user selected periodic background sync for one profile as the next feature
and chose a small config file over command-line settings. Approved
[spec 000009](spec/000009-linux-daemon-design.md) and
[decision 0008](decisions/0008-linux-daemon-proposal.md) are on
`feat/linux-daemon`. Agreed details include JSON config, an existing-profile
reference, explicit mode, a five-minute default interval, sequential scheduling,
safe signal cancellation, lifetime daemon exclusion, and offline status using
lock-observed ownership plus historical records. File watching and systemd
installation remain follow-ups. A fresh read-only builder check reported
"nothing important left"; self-review clarified startup guard-file ordering,
startup contention, and comparison against the original validated binding.
The paired [implementation plan](implementation-plans/000009-linux-daemon-plan.md)
is prepared for review with seven tasks covering leases, the locked existing-
profile API, config, scheduling, offline status, process lifecycle, and live
acceptance. Native execution is retained as the user's existing preference.
Next: review the plan before implementation. No Go code changed or tests ran
while preparing the spec and plan.

The user approved plan 000009 for native execution. Task 1 adds typed lock
contention and exclusive/shared-probe lifetime leases. Focused privatefs tests
and race tests pass, including a separate process and canonical parent aliases.
The remaining six tasks and full/local live verification are pending.

Task 2 adds locked existing-profile execution and offline inspection. Full
syncengine and race suites pass, including missing/rebound state rejection,
held-lock checks, incomplete download versus two-way validation, conflict
counts, uncertain upload preservation, and read-only Trash outcome recovery.
Normal one-shot execution shares the same engine body and remains covered.

Task 3 implements strict descriptor-validated Linux JSON configuration with
owner-only permissions, explicit modes, duration limits, duplicate/unknown-field
rejection, canonical profiles, and config/destination/reserved-record exclusion.
Focused config tests pass. Scheduling, status, binary/process, and live work
remain pending.

Task 4 implements the sequential loop, lifetime ownership, elapsed waits,
private advisory records, and safe cancellation/cleanup. Daemon/privatefs test
and race suites pass. Status cannot recreate a deleted profile: the shared
atomic writer now has an existing-parent-only entrypoint. Contradictory counts
before a recorded attempt completes are rejected by a reproduced regression.
Offline status, binary/process verification, and live acceptance remain pending.

Task 5 implements offline status and CLI wiring. Daemon, CLI, and command test
and race suites pass, covering stale records, invalid inspection, concurrent
readers, busy summaries, private-data redaction, partial errors, and faithful
operation counts. Status works without a token file or Drive access. Binary/
process acceptance and the dedicated-account live scenarios remain next.

Task 6 adds the foreground Linux binary and builds both commands. Real-process
acceptance passes SIGINT/SIGTERM during transfer and wait, forced-death restart,
profile aliases, manual sync/recovery exclusion, and shared-token contention.
Cancellation retains its identity through private Drive errors, including Go's
signal-specific cancellation cause. Formatting, lint (0 issues), full offline/
race suites, and both native builds pass. Live daemon acceptance remains next.

Task 7 adds gated actual-binary live download/upload scenarios and operator docs.
The first focused download passed in 76.96s. Upload independently verified Drive
bytes but its no-op snapshot preceded a later metadata reconciliation, changing
a directory mtime. The corrected test first establishes a zero-operation cycle;
focused upload then passed in 46.14s. The failed process was stopped and its
recorded owned fixture was subsequently cleaned with ownership checks. No
uncertain mutation was retried. No-op acceptance now checks all local file hashes
and file/directory mtimes across a subsequent cycle.

A shared-engine regression also proved cancellation could hide a cleanup failure;
close failures now join the original error through a sanitized message. Extended
Drive regressions preserve cancellation through folder/pin response decoding,
revision downloads, and upload initiation/transfer/completion. Focused tests pass.
The full enabled live aggregate and final independent review are pending.

The full enabled Linux live aggregate passed in 398.704s, including daemon
download (71.37s), daemon upload (46.57s), deletion, incremental/initial sync,
revision pinning, conditional-write probing, and upload/concurrent revision
recovery. Every scenario completed fixture-owned cleanup. The probe again showed
Drive accepting a stale version condition; existing documented race limits stand.

Independent read-only review of `ccfa67e..2040386` found one Important issue:
several shared engine and OAuth boundaries still discarded cancellation causes
above the fixed Drive client. No Critical/Minor findings or declined judgments
were reported; the reviewer accepted the plan and recorded implementation choices.
Regression tests reproduced cause loss in RunExisting uploads/file/folder creates,
upload staging and private-stage verification, conflict checks/downloads, Trash
descendant verification, and actual OAuth refresh. Fixed-message wrappers retain
those causes and genuine joined close failures while preserving uncertain intent,
verified stages, credentials, and no-blind-retry rules. A real SIGTERM subprocess
regression also covers a blocked folder creation with a retained create-intent.
Its fake HTTP handler initially hung on an unread request body; consuming that
body before blocking fixed the test server's cancellation detection. No production
endpoint or mutation behavior was changed for the test. Fresh full checks and a
second enabled live aggregate on the final fixes are pending.

## Minimal Linux daemon complete

All seven tasks of spec/plan 000009 are implemented on `feat/linux-daemon`.
The independent review's one Important finding is fixed with the regressions
listed above; no Critical, deferred Minor, or declined findings remain. The
single fix pass preserved cancellation causes throughout engine/authentication
boundaries and retains uncertain journals/stages without repeating mutations.

Final formatting, lint (0 issues), all 11 offline and race-tested packages, and
both native Linux builds pass on `e3b1d26`. The final explicitly enabled full
live suite passed in 410.947s using the dedicated local token: daemon download
76.08s, daemon upload 48.33s, deletion 47.49s, incremental 78.14s, initial 39.66s,
revision pinning 24.93s, conditional-write probe 33.59s, and upload/concurrent
revision recovery 62.71s. Every scenario completed fixture-owned cleanup. A
macOS arm64 cross-build passed before the final cancellation-only fixes; no
non-Linux daemon runtime acceptance is claimed. Credentials, generated IDs, and
synced contents were neither printed nor committed. README documents private
config, foreground lifecycle, historical status, modes, and operator actions.

The changes are committed locally; no PR, push, or merge has been requested for
this daemon branch. Next: choose integration, then a bounded systemd-user-service
or file-watching ticket. Runtime remains Linux-only, config changes require a
restart, and existing Drive conditional-write/revision-pin limitations remain.

Environment note: an early mise invocation reported removing older globally
installed Bun, Node, and Java versions. This was not requested; subsequent
commands used non-login shells and did not repeat it. Its cause was not
established. No unrelated repository changes were made.

## Linux daemon pull request

2026-10-24: the user authorized pushing `feat/linux-daemon` and opening a PR to
`main`. Fresh offline tests pass; the full offline/race/build/lint and dedicated-
token live results above remain the implementation verification. CI and
CodeRabbit review of the published branch are pending. Retrieve the summary,
inline comments, and review threads for the latest head before declaring ready;
fix valid findings and wait for fresh checks. Merging remains unauthorized.

[PR #15](https://github.com/torfstack/grove/pull/15) is open against `main`.
CodeRabbit explicitly skipped automatic review of `e852743`, stating that the
repository has fewer than 10 stars. Its successful status therefore does not
mean a review occurred. The summary was retrieved through GitHub; reviews,
inline comments, and GraphQL review threads are all empty. A manual review
trigger requires posting a PR comment, for which separate authorization is
required by this repository's feedback rules. Next: obtain that authorization,
trigger manual review, assess any findings, and verify the latest checks. CI
and CodeQL Go were still running during this inspection; dependency review
and CodeQL Actions passed. No merge is authorized.


2026-10-24: the user authorized the manual CodeRabbit trigger. The full review
of `ccfa67e..43e724b` completed at 12:17 UTC, selected all 56 changed files, and
reported low merge risk with no confirmed functional problems. Its three inline
findings are valid documentation corrections: remove local watching from current
daemon capabilities, replace unfinalized status/daemon command wording, and
advance this document's update date. All three are corrected, including adjacent
stale daemon-design/future-tense statements. No Go code changed. The complete
summary, submitted review, inline comments, and review threads were retrieved;
there are no additional findings to decline or defer. CI and security checks
passed on the reviewed head. Documentation validation passes; fresh CI and
CodeRabbit feedback for the documentation fix are the next check. The service
also reports that its one included review per hour has been consumed; verify
actual follow-up availability rather than treating a skipped check as review.

The three CodeRabbit documentation corrections are pushed in `3fc6d06`.
An authorized incremental review trigger was accepted, but the latest-head
summary then explicitly reported the hourly free OSS review limit: the next
included review is available around 13:10 UTC on 2026-10-24 (the bot reported
50 minutes remaining at approximately 12:20 UTC). This is a rate-limited skip,
not a completed re-review, despite its successful check status. The original
full review remains applicable to unchanged Go code; only documentation changed
after it. No new submitted review or additional findings have appeared. The
three original threads remain open; fixes are pushed, with no replies or thread
resolutions posted. Next: after the allowance resets, trigger an incremental
review on the current head, inspect its summary/comments/threads, and handle any
new findings. CI for the documentation follow-up is checked independently.
No merge is authorized.

A subsequent GitHub inspection of `20392df` reports all three original review
threads resolved; no thread-resolution API call or reply was made by this agent.
Linux CI, CodeQL Go/Actions, and dependency review all passed on that head. The
only submitted CodeRabbit review still covers `43e724b`; subsequent changes are
documentation-only. The latest automatic review is again skipped by the OSS
manual-trigger policy, and the attempted incremental review was explicitly
rate-limited as recorded above. No functional findings or unresolved actionable
comments remain; the remaining limitation is confirmation of the doc-only fixes
by a fresh manual review after the allowance resets.

2026-10-24: the user changed the workflow to one completed CodeRabbit review per
PR. [Decision 0009](decisions/0009-single-coderabbit-review.md) and AGENTS.md now
record this rule: assess and fix the initial findings, verify, and wait for CI;
do not request or wait for CodeRabbit re-review unless explicitly asked. This
supersedes the quota-reset/re-review next steps above. PR #15 has already received
its one full review; all three findings are fixed and its threads are resolved.
The user also requested a fresh actual-groved live test, so both dedicated-token
periodic download/upload scenarios are running. No merge is authorized.

The fresh user-requested actual-groved live run passed in 117.606s on 2026-10-24:
`TestDaemonPeriodicDownloadLive` 72.25s and `TestDaemonPeriodicUploadLive` 45.35s.
Both built and launched the actual binaries with private disposable profiles and
the dedicated test token, independently verified content and subsequent no-op
cycles, checked offline status and SIGTERM shutdown, and completed fixture-owned
cleanup. The download scenario also verified busy token exclusion during an
owned remote mutation. No credentials, generated remote IDs, or synced contents
were printed. No Go code changed. CI/security checks passed on `af062da`; this
follow-up records results only. Under decision 0009, no additional CodeRabbit
review is requested or awaited.

## Systemd user-service pull request

2026-10-24: the user authorized pushing `feat/systemd-user-service` and opening
[PR #16](https://github.com/torfstack/grove/pull/16) against `main`. Published
implementation head is `f5c2f42`. The initial push was rejected by automatic
approval review as private/unverified egress; read-only checks established that
origin exactly matched the recorded `git@github.com:torfstack/grove.git` and
GitHub reported the repository public. The reviewed retry succeeded.

CodeRabbit skipped automatic review because the repository has fewer than ten
stars. The user explicitly authorized the manual `@coderabbitai review` comment,
which was posted. The initial full review completed at 13:47 UTC on `f5c2f42`,
selected all 11 changed files, and reported low merge risk with one actionable
Minor finding. The completed summary, submitted review, inline comment, and
all review threads were retrieved; no additional findings appeared.

The finding is valid: the guide's unit-path check used systemd-analyze's local
path list instead of the running manager's UnitPath. It now recommends
`systemctl --user show -p UnitPath --value`, requires the shell-derived install
directory to match a reported path, and explains rechecking newly created
directories after daemon-reload. The actual command was verified with matching
and deliberately mismatched shell XDG_CONFIG_HOME; installed systemd docs confirm
the distinction. No unit or Go behavior changed. Documentation checks pass.
Linux CI, dependency review, and CodeQL Go/Actions passed on the reviewed head;
fresh Linux CI, dependency review, and CodeQL Go/Actions all passed on the
documentation fix `a5c7d03`. The original thread is now outdated but remains open;
no reply or manual thread-resolution action was posted. All actionable findings
are addressed. Per decision 0009, no second CodeRabbit review is requested or
awaited. This follow-up records results only. No merge is authorized.

Local watching Task 1: strict optional watch_local configuration and offline
historical status are implemented; daemon/CLI tests pass. Baseline full offline
tests passed with filesystem/loopback access and a temporary Go cache. Watching
runtime is not connected yet.

Task 2 adds deterministic debounce/periodic/retry scheduling. Scheduler tests
and daemon race tests pass. Selectable timers extend the existing clock contract
rather than replacing periodic-only test injection.

Task 3 implements recursive descriptor-anchored Linux inotify watching using
existing x/sys. Native and daemon race tests pass, including overflow repair,
repeated directory replacement, symlink exclusion, root identity, and reader
shutdown. Registration requires /proc/self/fd. Event hints never resolve paths
for engine operations; full engine scans remain authoritative.

Task 4 connects watch hints to one sequential shared-engine worker while the
scheduler continues draining events. Daemon/CLI/process race tests pass, covering
completion-boundary edits, retry limits, historical deadlines, cancellation,
watcher errors, and cleanup-before-lease-release ordering. Periodic-only behavior
retains its original wait path and shares attempt classification.

Task 5 real subprocess acceptance passes, including three repeated race-enabled
runs: Grove's own downloads settle after one no-op follow-up, an edit during a
blocked transfer is observed, SIGINT/SIGTERM clean up, root replacement stops
visibly, and lifetime ownership is released. A reproduced transient-probe
ENOENT during watcher enumeration now triggers coverage repair; deterministic
and actual-directory-to-symlink swap regressions pass. No shared engine mutation
semantics changed.

Task 6 adds gated actual-binary watch-triggered upload acceptance and operator
records. Disabled-mode acceptance reports an explicit SKIP without token/network
use; authenticated local watching remains untested. Fresh fmt, lint (0 issues),
all 11 offline/race packages, both Linux builds, and diff checks pass. Both
binaries also cross-build for macOS arm64; no non-Linux runtime is claimed.
The independent whole-change review and hosted PR review/checks are next.

Independent whole-change review of `23c583b..dc8cb58` found two Important
shutdown-ordering defects and no Critical findings. Deterministic regressions
reproduced both: SIGTERM masked queued/late watcher failures, and cancellation
during reconciliation became fatal while genuine completion cleanup errors
could be lost. The single fix pass preserves sticky reader errors after join,
suppresses only expected closed-reader cancellation, and distinguishes graceful
repair cancellation from genuine engine/repair faults. All five new shutdown/
read-error regressions pass. Fresh fmt, lint (0 issues), full offline/race suites,
both Linux builds, both macOS arm64 cross-builds, and diff checks pass on the fixes.
The reviewer also identified an overstated verification sentence: the plan now
explicitly records that directory churn does not forcibly inject kernel watch-
descriptor reuse/stale-event ordering. Authenticated watching remains gated/unrun;
network filesystems, non-Linux runtime, and adversarial transient reparenting
are not claimed. Event hints never become mutation paths or deletion authority.
The user authorized PR publication, completed CodeRabbit review, fixes, passing
CI, and merge; publication and hosted review/checks are next.

## Local watching PR and dependency maintenance

[PR #17](https://github.com/torfstack/grove/pull/17) is published. Linux CI,
CodeQL Go/Actions, and dependency review passed on implementation head `9fb0c84`.
CodeRabbit skipped automatic review for the repository's star count; the manual
request at 14:28 UTC was rate-limited. The retry at 17:09 UTC was accepted and
completed its initial full review of `23c583b..9fb0c84` at 17:16 UTC. A successful skipped
status is not review evidence; retrieve the completed summary, inline comments,
and review threads before merging. The user has authorized that workflow and
merge after fixes and fresh CI; no second completed review is required.

The next bounded maintenance work is completed: PR #11 merged at `002a20e`
(x/sys v0.49.0) and PR #14 at `750ba2a` (immutable mise/CodeQL action updates).
Their completed CodeRabbit summaries were retrieved and assessed: PR #11 reviewed
`1d5f7ded` with unchanged go.mod/go.sum in the later pre-update head; PR #14
reviewed `c974994`. Both selected the changed reviewable source files and reported
no actionable findings. Submitted reviews, inline-comment APIs, and GraphQL
review threads were retrieved and empty; comment-based completed summaries,
not successful status checks, establish the reviews. No re-review was requested.
Both branches were refreshed against main, all hosted CI/security checks passed,
and each merge used the verified head. Upstream release tags resolve to the
pinned action commits, with CodeQL init/analyze aligned.

Combined local validation of the dependency updates with watching passes module
verification, fmt, lint, full offline/race suites, both Linux builds, and macOS
arm64 cross-builds. The watcher branch now includes both dependency merges.
A further regression reproduced attempt-local timeout/cancellation being labelled
as daemon cancellation after attempt-classification extraction; classification
now requires an actual daemon cancellation cause. The regression, daemon race
suite, and fresh full integrated fmt/lint/offline/race/build checks pass.
This regression-backed fix follows the completed initial CodeRabbit review;
push and fresh CI are next.
Authenticated watching remains gated and unrun.

PR #17's initial full CodeRabbit review completed at 17:16 UTC on `9fb0c84`,
selected all 31 changed files, and reported minimal merge risk with no actionable
findings. The entire summary, submitted-review API, inline comments, and GraphQL
review threads were retrieved; the latter three are empty. There are no findings
to decline or defer. The initial review covers the implementation; the narrow
attempt-timeout classification regression/fix and already-reviewed dependency
merges follow it. Full integrated local checks pass. Per decision 0009, no second
CodeRabbit review is requested or awaited. Push and fresh integrated-head CI are
next, then merge under the user's standing authorization.

The primary checkout is now clean on main with both dependency merges. Its
pre-implementation design-only drafts were preserved in the named Git stash
`preserve local watching design drafts before implementation integration`;
those approved files are also committed on the watcher branch. The implementation
worktree remains `/tmp/grove-local-watching` until integration completes.

All hosted CI/security checks passed on `7237fbb`, after the completed initial
review. A final deterministic regression then reproduced a remaining native
coverage-worker ordering: a genuine repair error returned concurrently with
cancellation was discarded. The worker now suppresses cancellation-only errors
and retains genuine faults for the scheduler's post-join drain. The failing
`TestWatchRepairFailureDuringCancellation` passes with the fix. Fresh fmt, lint
(0 issues), full offline/race suites, both Linux builds, both macOS arm64 cross-
builds, and diff checks pass. This narrow shutdown fix is pushed for fresh CI;
no additional CodeRabbit review is requested under decision 0009.

Remote detection Task 1 implements strict optional remote_poll_interval and
historical interval/check timestamp/outcome fields. Existing disabled behavior
remains valid; enabled intervals must be at least ten seconds. Daemon/CLI normal
and race coverage passes. Remote polling execution is not connected yet.

Remote detection Task 2 adds the separate bounded changes HTTP interface, opaque
cursor encoding, scoped My Drive queries and checked response cleanup. Drive
normal/race tests pass. The response-cap regression also fails when the 1 MiB
cap is deliberately weakened, then passes with the cap restored. No Drive IDs
or response bodies are exposed by the new boundary.

Remote detection Task 3 adds locked read-only feed checks, original-binding
comparison, complete pagination and cursor preservation on partial/error results.
Sync-engine normal/race tests pass, including real fake-HTTP pagination, missing/
rebound/busy profiles, unchanged state bytes and genuine close failures joined
with cancellation. A typed cleanup fault lets the daemon stop rather than hide
resource failures as ordinary remote-check errors.

Remote detection Task 4 implements the bounded polling source and a cancellation-
aware job gate. Daemon race tests pass for immediate initialization, check budget,
cursor retention, partial dirty hints, failed/busy retries, drained observations,
and sticky cleanup faults preserved after source join. Native watcher tests remain
green; remote execution integration is next.

Remote detection Task 5 integrates optional local/remote sources with one existing
scheduler and serialized API gate. Affected daemon/CLI/process race suites pass.
Real daemon processes detect fake remote edits before a ten-minute periodic
interval, then empty checks preserve the prior attempt timestamp/counts. Remote-
only, combined-source and in-flight-check SIGTERM cases release lifetime ownership.
Remote observations persist only across valid record phases and do not erase
normal sync history. Live remote detection and final review remain next.
