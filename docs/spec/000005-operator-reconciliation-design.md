# 000005: Operator reconciliation for interrupted uploads

Status: approved by the user on 2026-10-11; implemented on
`feat/operator-reconciliation`; five independent review findings were fixed and
verified. See the paired plan and STATUS for review limits and verification.

## Intent and agreed scope

After merging local uploads in PR #7, the user selected operator recovery as
the next task. The agreed first slice adds `recovery inspect`, `recovery retry`,
and `recovery export`. Users must be able to understand a pending upload,
retry its existing recovery transaction without starting unrelated sync work,
and retrieve verified staged content when ambiguity prevents progress.

This does not guarantee that every blocked profile can be unblocked. An old
remote head or a missing allocated create ID does not prove an uncertain request
has settled. Export preserves access to bytes without authorizing another write.

The shared Go engine owns the behavior for both CLI and future daemon clients.
Linux is the primary target. Grove remains pre-release; earlier-release migration
support is unnecessary, but current-run interruption recovery is required.

## Approach and alternatives

Recommend a recovery-only service boundary over the existing upload journal.
It exposes structured diagnostic results and invokes the existing recovery
phases without running the sync planner or executing newly discovered work.

A diagnostic command that merely recommends `sync --two-way` is smaller, but
that command can perform unrelated uploads after recovery. A force-abandon
command appears to unblock more cases, but cannot establish that an in-flight
request will not complete later. Neither alternative meets the agreed boundary.

## CLI and service contract

All commands require the existing profile binding flags: `--profile-dir`,
`--remote-root`, `--local-dir`, and `--token-file`. They operate only on an
existing, populated profile. Missing, malformed, or mismatched state is an error;
commands must not initialize a profile or repair malformed records by guessing.

- `grove recovery inspect`: offline inspection of the pending upload/create
  journal and its recorded stages. No authentication or remote requests. Reports
  operation kind, phase, whether recovery is pending, stage availability and
  verification status, recorded competitor count, and appropriate next actions.
  It distinguishes recorded facts from remote conditions requiring retry.
- `grove recovery retry`: explicitly permits recovery-related remote pin changes
  and local journal/baseline changes. Requires read-write authentication. Processes
  only the transaction present at command start. Never sends new file content,
  creates remote objects, allocates replacement IDs, applies conflict policy,
  or executes the sync planner. It can capture a competitor into a durable ask
  conflict; resolution remains an explicit existing conflict workflow.
- `grove recovery export --candidate local|cloud --output <file>`: copies a
  recorded, fully verified stage from the pending transaction to a new private
  local file. Offline; does not alter profile state, synced files, or Drive.
  Cloud export requires a completed verified capture, not a partial download.
  Folder creates have no bytes to export. Export of ordinary persisted conflicts
  and unrecorded artifacts is outside this slice.

Service methods return structured reports; CLI formatting does not classify
errors or implement recovery decisions. Reports use stable reason codes and
action names. Do not expose credentials, session URLs, operation markers,
generated Drive IDs, revision IDs, hashes, synced contents, or internal stage
paths. Output identifies this profile's single pending operation by kind and
phase; source paths are omitted from default output. Underlying provider errors
must be sanitized before display.

Inspection succeeds when it can diagnose valid state, including a missing or
changed stage. Retry returns success when there is no pending transaction or
when the original transaction is fully recorded/cleaned up; it distinguishes
completion from conversion to an unresolved conflict. A retained transaction
returns a typed blocked error and a report. Provider, persistence, cancellation,
and resource-close failures remain errors, never successful reconciliation.
Export succeeds only after verified bytes are durably published.

## Recovery evidence and limits

| Condition | Required behavior |
| --- | --- |
| No transaction | Report no pending recovery; perform no remote calls |
| Pending download/move/replace | Report unsupported by this command; recommend normal sync recovery |
| Owned stage missing, changed, or lacking verification | Report unavailable; refuse export and unsafe recovery; preserve records |
| Staged/pin-intent/pinned before content request | Revalidate original preconditions; release only a Grove-owned preflight pin when allowed; retire the transaction safely |
| Uncertain update, unchanged head | Retain original intent and stage; explain that completion is not established |
| Uncertain create, ID not found | Retain allocated identity and stage; do not create again |
| Apparent update success | Revalidate identity, ownership, name, parent, type, revision and content; require anchored revision history and independent byte verification before baseline commit |
| Apparent create success | Require allocated identity, operation marker, ownership, name, parent, type and verified content; do not adopt another object |
| One recoverable competitor | Pin, capture and verify through journaled phases, then publish a durable ask conflict |
| Multiple competitors | Preserve identities and pins; report selection unsupported; keep transaction blocked |
| Incomplete revision history | Fresh retry may reassess history; never clear uncertainty solely because an entry disappears |
| Committed transaction | Finish owned cleanup only; preserve stages referenced by conflicts |

Checksums alone do not prove authorship. Reuse the accepted revision-aware
reconciliation model in spec 000004, with its documented concurrent-write limits.
Remote metadata and independent content verification must describe the same
stable revision. A changed identity, path, ownership, type, head or content stops
recovery. Metadata version-only changes follow the existing accepted rule.

Reassessment of a previously unverified history must require both recorded
anchors and valid timestamps. Persist newly discovered competitors before any
pin/capture work. Previously recorded competitor identities remain obligations;
their absence never proves safety. Multiple competitors stay out of scope.

## State, locking and interruption safety

Reuse validated State and Journal records. Diagnostic status is derived from
records and checked artifacts rather than stored as a second source of truth.
Use typed recovery errors shared by normal sync and recovery services so the
same blocked condition receives the same classification.

Acquire the profile lock for all three operations. Retry also uses the existing
destination lease and token-session locking, holding exclusion throughout
recovery. Inspect/export do not write the sync destination or acquire OAuth
sessions. Check cancellation during reads and streaming copies.

Persist intent before remote pin/capture changes; save baselines atomically.
Do not clear a transaction before its remaining owned cleanup is recoverable.
Use existing identity/hash ownership checks and preserve conflict-owned stages.
Pre-existing or unknown-ownership pins remain pinned. Respect the accepted
200-pin limit and report failures without abandoning the journal.

## Export publication

Require an existing output parent and a previously nonexistent output file,
outside the profile and every registered sync destination. Reject symlink or
filesystem-alias escapes using existing canonicalization and rooted filesystem
boundaries. Validate exclusions under destination-registry coordination so a
concurrent profile cannot claim the export directory during publication.

Open the recorded stage through the profile root; verify file identity before
and after copying and hash/size while streaming. Use a private temporary file
in the output parent, mode 0600, then publish without replacement only after
verification and file sync. Sync the parent directory. Existing output files,
including symlinks, must never be overwritten. Bound memory independently of
file size. Remove only the temporary file created and identified by this attempt
on handled failure. A process crash may leave a private partial temporary file;
do not treat it as a complete export or remove unknown artifacts on retry.

Exports are copies, never moves. The original stage and journal remain intact
on success and failure. Errors after publication must report that a file may
exist; retries still refuse replacement.

## Architecture and focused changes

Add recovery service/report types and CLI wiring alongside conflict controls.
Extract only the setup required to share canonical binding, profile exclusion,
destination leasing, rooted access and persistence with normal sync. Keep the
executor's upload recovery phases as the source of recovery behavior.

Audit those phases against the evidence table, particularly recovered create
structure/content validation, recovered update independent verification,
history reassessment, and journal retirement before stage cleanup. Fix gaps
necessary for this contract with regression coverage; avoid unrelated refactors.
No second sync engine, generic workflow framework, daemon or new persisted
recovery state machine is introduced.

## Verification and completion

Deterministic tests must demonstrate:

- Offline inspect/export make no provider calls and leave state unchanged.
- Every supported phase has a useful diagnosis; corrupted state fails safely.
- Retry touches only the original transaction even when unrelated local edits
  and remote additions would otherwise produce new sync operations.
- Lost responses with an old head or absent allocated ID retain identity and
  stages with zero content updates, creates, or replacement ID allocations.
- Proven outcomes validate structure and independently verify bytes; mismatched
  markers, ownership, parent/name/type, content, or moving heads remain blocked.
- One competitor becomes an ask conflict regardless of configured policy;
  multiple competitors and missing anchors remain preserved and blocked.
- Fresh complete history can recover a prior listing failure without dropping
  recorded competitor obligations.
- Fault injection at pin/capture, baseline-save and cleanup boundaries resumes
  safely, including cleanup failures and pre-existing pins.
- Export handles zero-byte and streamed files, partial/changed stages, existing
  files, symlinks, aliases, destination collisions, cancellation and publication
  failures without changing source data or overwriting targets.
- Concurrent sync/retry/export obey profile exclusion and destination safety.

Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`,
native build and Linux cross-build for implementation. Linux offline CI is
runtime evidence; cross-build alone is not Linux runtime acceptance. Live tests
remain opt-in and require dedicated test credentials, disposable directories,
and fixture-owned remote data. This spec does not authorize a live run.

Update README with command examples and the explicit indefinite-blocking limit.
Implementation is complete when these behaviors and tests pass and review
findings are addressed under the repository's PR workflow.

## Deferred work

Force-abandon/reset, terminal proof for in-flight requests, multiple-competitor
selection/export, unowned artifact reconciliation, deletion propagation, local
rename detection, revision-quota redesign, and daemon orchestration need separate
designs. Users of an inconclusive transaction can export verified bytes and
retry later; this slice does not claim that exporting permits safe profile reset.

The paired [implementation plan](../implementation-plans/000005-operator-reconciliation-plan.md)
links here. The user approved native execution; see STATUS for actual verification
and review outcomes.
