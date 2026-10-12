# Project status

Updated: 2026-10-11.

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
path; this rare error can leave an empty private directory. Fresh hosted checks
CodeRabbit then found that a directory-handle close failure after successful
publication could be reported as if no output existed. Publication success is
now tracked separately from close errors, with a regression that verifies the
exported bytes and published flag. Formatting, lint, full offline tests, race
tests, native build and Linux cross-build passed. Fresh CI, CodeQL, dependency
review, and CodeRabbit all passed on `98009ef`. CodeRabbit's latest review covered
`1e22d67` through `98009ef` and reported no actionable comments; the temporary
directory thread is resolved. The rare Lstat ownership-unknown cleanup case is
recorded above. New hosted review is pending after this status update commit.
