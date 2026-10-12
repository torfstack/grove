# Operator Reconciliation Implementation Plan

> **For agentic workers:** Use `superpowers:executing-plans` for native execution or `superpowers:subagent-driven-development` if the user selects delegated execution. Implement tasks in order; checkbox steps track progress.

**Goal:** Explain pending uploads, recover only their existing transactions, and export verified staged bytes safely.

**Architecture:** Recovery services use the existing upload executor and validated journal. Reports and typed errors belong to the shared engine; CLI code formats them. Export has a separate rooted, streaming publication boundary coordinated with the destination registry.

**Tech Stack:** Go 1.27.2, Cobra, existing Drive/privatefs packages; golangci-lint 2.14.0 and GitHub CLI 2.102.0 through mise. No new dependencies.

**Spec:** [Approved spec 000005](../spec/000005-operator-reconciliation-design.md).

## Global constraints

- Linux is the primary target; platform operations stay behind existing boundaries.
- All commands require `--profile-dir`, `--remote-root`, `--local-dir`, and `--token-file` and an existing populated profile.
- Inspection and export are offline and leave profile state unchanged.
- Retry never sends new file content, creates remote objects, allocates replacement IDs, applies conflict policy, or executes the sync planner.
- Old heads, absent allocated IDs, missing history and checksums alone do not establish that an uncertain request has settled.
- Preserve current-run recovery; earlier-release migrations are unnecessary while pre-release.
- Default reports omit source paths, internal artifact paths, hashes, operation markers, Drive/revision IDs, credentials, session URLs and contents.
- Export mode is 0600, output must not exist, output parent must exist, and output must be outside profiles and registered sync destinations.
- All operations hold profile exclusion; retry also holds destination and token exclusion; export coordinates with the registry during publication.
- No live run is authorized by this plan. No force-abandon, multi-competitor selection, daemon or unrelated refactor.

## Review focus

1. A valid populated profile may have legacy `Pending` download work rather than a Transaction: diagnose unsupported recovery without treating it as idle (Task 1).
2. A committed transaction may reference a stage already removed by prior cleanup: allow cleanup completion while refusing export of absent bytes (Tasks 1–2).
3. Errors can contain provider URLs and generated IDs: structured classification must preserve error identity without printing sensitive causes (Tasks 1 and 5).
4. An export parent can be renamed or a competing profile can register it after validation: lock/root boundaries must protect publication and preserve exclusions (Task 4).
5. A concurrent writer can replace a source pathname after it is opened: compare descriptor and rooted-path identities before and after the copy (Task 4).

## File map

- New `internal/syncengine/recovery_report.go`: report enums, reason codes and typed blocked errors.
- New `internal/syncengine/recovery_service.go`: offline inspect and recovery-only retry.
- New `internal/syncengine/recovery_export.go`: verified streaming export and publication.
- New `internal/syncengine/export_destination.go`: output validation/lease using destination registry and directory locks.
- Modify `upload_recovery.go`, `journal_validate.go`, `upload_execute.go`: recovery evidence and durable cleanup retirement; keep normal sync on the same implementation.
- New `upload_recovery_verify.go`: stable remote structure/content and history checks used by recovery.
- Modify `service.go` only for small reusable registry/setup helpers when needed; no general runner abstraction.
- Modify `destination.go` only to share registry loading/validation with export.
- New `internal/cli/recovery.go`; modify `internal/cli/root.go` and `cmd/grove/main.go` to wire service methods.
- Tests next to each new file; extend existing upload recovery, state and destination tests.
- README, STATUS, spec, decision and roadmap updates describe completed behavior and actual verification.

## Task 1: Structured recovery diagnosis and offline inspection

**Files:** Create `recovery_report.go`, `recovery_service.go`, `recovery_report_test.go`, `recovery_service_test.go`; modify `conflict_service.go` only if existing-profile loading requires a narrowly shared helper.

**Interfaces:**

```go
type RecoveryReason string
type RecoveryAction string
type RecoveryStageStatus string
type RecoveryOutcome string
type RecoveryStage struct { Status RecoveryStageStatus }
type RecoveryReport struct {
    Pending bool
    Operation OperationKind
    Phase string
    Reason RecoveryReason
    Actions []RecoveryAction
    Local, Cloud RecoveryStage
    CompetitorCount int
    Outcome RecoveryOutcome
}
type RecoveryBlockedError struct { Reason RecoveryReason }
func (e *RecoveryBlockedError) Error() string
func (s Service) InspectRecovery(ctx context.Context, opts Options) (RecoveryReport, error)
func inspectRecoveryState(ctx context.Context, profile string, state State) (RecoveryReport, error)
```

Reason values: `none`, `remote-check-required`, `unsupported-operation`, `stage-unavailable`, `uncertain-write`, `remote-changed`, `history-incomplete`, `multiple-competitors`, `capture-pending`, `cleanup-pending`. Action values: `retry`, `export-local`, `export-cloud`, `sync`, `resolve-conflict`. Stage values: `none`, `verified`, `unavailable`, `partial`. Outcome values: `none`, `pending`, `completed`, `conflict-pending`.

- [x] Write `TestInspectRecoveryOffline` with a panic-on-open provider: valid idle state returns `Pending=false`, reason `none`; upload/create phases return their recorded kind/phase and competitor count; state bytes remain identical. Inspect must not claim fresh remote observations.
- [x] Add `TestInspectRecoveryStageStates`: missing, changed, symlink and identity-mismatched stages report unavailable; capture-intent cloud bytes are partial; verified captures are exportable; folders have no stage; committed missing stages remain diagnosable. Add `TestInspectRecoveryRejectsInvalidProfile` for absent state, binding mismatch, malformed journals and unpopulated profiles, and `TestInspectRecoveryPendingDownloadUnsupported`.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'TestInspectRecovery' -count=1`; confirm failures exercise absent behavior.
- [x] Implement report types and offline inspection using canonical binding and profile lock. Check `state.json` exists before loading; no state creation. Use context-aware rooted stage reads with identity/hash checks. Classify recorded phase only; don't turn an unverified artifact into a verified candidate.
- [x] Add `TestRecoveryBlockedErrorSanitized`: error string contains only fixed safe copy and a stable reason; arbitrary provider details never enter report fields. Preserve cancellation with `errors.Is`. Test profile lock contention and unchanged state on cancellation.
- [x] Run the focused tests and `mise run lint`; commit `feat(sync): inspect pending upload recovery offline`.

## Task 2: Evidence-based upload recovery and durable retirement

**Files:** Modify `upload_recovery.go`, `journal_validate.go`, `upload_execute.go`; create `upload_recovery_verify.go`, `upload_recovery_verify_test.go`; extend `upload_execute_test.go`, `state_test.go`.

**Interfaces:** Existing `(*executor).recoverUpload(context.Context) error` remains the shared entry point. Add:

```go
func (e *executor) verifyRecoveredRemote(ctx context.Context, j Journal, current drive.File) (Completed, error)
func (e *executor) recoverPreSend(ctx context.Context, j Journal) error
func (e *executor) cleanupRetiredUpload(ctx context.Context, j Journal) error
```

Return `*RecoveryBlockedError` for inconclusive conditions; preserve provider/persistence/cancellation error identity for callers without incorporating sensitive causes in user-facing copy. Add a `retired` upload/create journal phase with no result baseline for safely cancelled pre-send work. It records cleanup obligations without applying a baseline. Validate it explicitly; do not confuse it with `committed`.

- [x] Write table tests `TestRecoveredRemoteRequiresStructureAndStableBytes`: independently downloaded SHA-256 must match stage, metadata before/after must identify the same revision/content; ID, marker for creates, ownership, trash status, parent/name/type mismatch, same-MD5 wrong bytes and moving head all block without baseline updates. Folder creates validate structure with no media request. Uploaded/verified phases must still validate remote outcomes before commit.
- [x] Write `TestRecoveryRetainsUncertainIdentity`: old head and not-found create ID retain journal and stage; assert zero GenerateIDs, UpdateContent, CreateWithID calls. Retain existing accepted revision-aware outcome model rather than inventing checksum authorship proof.
- [x] Write `TestRecoveryRechecksHistory`: prior unverified history followed by fresh valid anchored history progresses; absent anchors/invalid timestamps remain blocked; merge and durably retain old/new competitors. One competitor captures; multiple competitors remain pinned and blocked. Persist a reconciled `After` before capture, including from upload-intent, so capture never indexes an absent result.
- [x] Write `TestRecoveryRetiredCleanupResumes`: save the retired phase before removal; failure saving retirement makes no deletion; removal or clear-save failure retains a valid restartable journal; missing already-removed stages complete retirement. Pre-existing and unknown pin ownership remain protected. Test faults at pin/capture, result-save and committed cleanup; conflict-owned stages survive.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'TestRecoveredRemote|TestRecoveryRetains|TestRecoveryRechecks|TestRecoveryRetired' -count=1`; confirm regression failures before fixing.
- [x] Implement structure and streaming independent verification, anchored history reassessment and typed errors. Reuse `contextReader`, existing Drive interfaces and journal saves. Missing recorded competitors never disappear from obligations. Pre-send recovery transitions to retired after safe precondition/pin handling, cleans owned stages, then clears the journal. Committed cleanup tolerates absent already-removed owned artifacts.
- [x] Run focused tests, existing upload/conflict/state tests and `mise run lint`; commit `fix(sync): verify recovered outcomes and retain cleanup obligations`.

## Task 3: Recovery-only retry service

**Files:** Modify `recovery_service.go`, `service.go` as needed; extend `recovery_service_test.go`.

**Interfaces:** `func (s Service) RetryRecovery(ctx context.Context, opts Options) (RecoveryReport, error)`. Uses Tasks 1–2's report and shared recovery executor. It never calls `Service.Run`, `Scan`, planners, or conflict-resolution execution.

- [x] Write `TestRetryRecoveryOnlyOriginalTransaction`: fixtures include unrelated local edits/remote additions and keep-local/keep-cloud policies; provider spies prohibit content writes, creates, ID allocation and tree listing. Confirm recovery completion or one durable ask conflict, with no unrelated work and no policy application.
- [x] Write `TestRetryRecoveryResourceBoundaries`: idle/unsupported states open no API; supported work opens read-write API, holds profile/destination/token exclusion through completion, and closes resources on all exits. Include provider, save, close and cancellation errors; no error is reported as completed. Reuse existing process exclusion fixtures for concurrent sync/retry.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'TestRetryRecovery' -count=1`; confirm failures.
- [x] Implement existing-profile loading, destination lease, rooted destination and executor setup; run only the starting transaction. Return fresh diagnosis with typed blocked errors for retained work. Distinguish completed from conflict-pending by changes caused by that transaction, rather than preexisting unrelated conflicts. Sanitize CLI-visible failures at the report boundary while retaining error identity where safe.
- [x] Run focused recovery and service tests with race detection, then lint; commit `feat(sync): retry only the pending recovery transaction`.

## Task 4: Offline export with coordinated publication

**Files:** Create `recovery_export.go`, `export_destination.go`, `recovery_export_test.go`, `export_destination_test.go`; modify `destination.go` to share registry reading if needed.

**Interfaces:**

```go
type RecoveryExportOptions struct { Candidate ConflictSide; Output string }
func (s Service) ExportRecovery(ctx context.Context, opts Options, export RecoveryExportOptions) error
func acquireExportDestination(registry, profile, localDir, output string) (*exportDestination, error)
func (d *exportDestination) Close() error
```

`exportDestination` privately owns parent `*os.Root`, target basename, registry/directory locks. Candidate accepts SideLocal/SideCloud only. No report exposes internal identities or filenames.

- [x] Write `TestExportRecoveryOfflineCopiesVerifiedStage`: local/cloud verified stages, zero-byte and streamed large files; output bytes match, mode 0600; source/state unchanged; API opener panics. Cloud partial capture and folder candidates refuse export.
- [x] Write `TestExportRecoveryRejectsUnsafeOutput`: existing files/symlinks, nonexistent parent, profile descendants, bound local destination and all registered destinations, case/filesystem aliases and symlink escapes; no output or state mutation. Include malformed registry fail-closed behavior.
- [x] Write `TestExportPublicationRaces`: registry registration races, renamed parent, changed/replaced source, destination created just before publish, cancellation/read/write/file-sync/link/directory-sync/close failures. Assert no replacement, no successful incomplete export, identity-owned temp cleanup only, and safe notice after publication errors. Use private injectable I/O hooks in export tests for faults, not global mutable hooks.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'TestExport' -count=1`; confirm failures.
- [x] Implement locking in order: profile, ancestor directory locks from root toward parent, output-parent directory exclusion, registry guard. Registry guard remains held through publication; registerDestination never waits on directory locks while holding it, avoiding reverse-order deadlocks. Check all registry destinations and the bound destination; do not register export paths as profiles. Prevent export inside any registered profile as well. Release locks in reverse order and report close failures.
- [x] Copy from rooted descriptor to exclusive identified temp using context-aware fixed-buffer streaming, check descriptor/path identities and SHA-256/MD5/size, fsync, publish by no-replace hard link, remove identified temp, fsync parent. Fail safely if filesystem lacks publication support. Compare output parent root identity with canonical pathname before publication and reject detectable rename/alias changes. Never follow target symlinks or move original stages. Post-link failures explicitly say the output may exist.
- [x] Run export/destination tests with race detection and lint; commit `feat(sync): export verified recovery stages without overwriting files`.

## Task 5: CLI and operator documentation

**Files:** Create `internal/cli/recovery.go`, `internal/cli/recovery_test.go`; modify `internal/cli/root.go`, `cmd/grove/main.go`, `README.md`.

**Interfaces:** Add Services fields `InspectRecovery`, `RetryRecovery` with Task 1/3 signatures and `ExportRecovery` with Task 4 signature. `func newRecovery(services Services) *cobra.Command` installs inspect/retry/export using existing profile flag validation.

- [x] Write `TestRecoveryCLICommands`: verify all binding arguments and export candidate/output pass to the right method; reject missing flags, positional args, invalid candidates and unavailable services. Inspect/retry format deterministic report fields and recommended actions. Blocked retry prints its safe report and returns an error; success distinguishes no work, completion and pending conflict.
- [x] Add `TestRecoveryCLIRedactsCauses`: fake errors contain token-shaped text, URLs, Drive IDs, hashes and stage/source paths; captured stdout/stderr and returned display error reveal none. Cancellation and resource errors never produce success copy. Export errors after publication retain the safe output-may-exist warning.
- [x] Run `mise exec -- go test ./internal/cli -run 'TestRecoveryCLI' -count=1`; confirm failures.
- [x] Implement Cobra wiring and main service injection. Format only engine reports; map unexpected causes to safe fixed messages without string matching. Preserve safe typed cancellation/blocked/publication categories. Avoid raw error wrapping in display strings.
- [x] Document examples using placeholder bindings/output, separate retry from normal sync, explain private exports and permanent blocking limits, explain follow-up explicit conflict resolution and unsupported multi-competitor recovery. No real IDs, paths to credentials, or contents in docs.
- [x] Run CLI and syncengine tests and lint; commit `feat(cli): expose recovery inspection retry and export`.

## Task 6: Whole-slice verification and review handoff

**Files:** `docs/STATUS.md`, `docs/ROADMAP.md`, `docs/decisions/0005-operator-reconciliation-proposal.md`, spec/plan progress records; implementation tests only if review exposes gaps.

- [x] Run `mise run fmt`, `git diff --check`, `mise run lint`, `mise run test`, `mise run test-race`, `mise run build` in sequence and record actual outcomes. Cross-build with `mise exec -- env GOOS=linux GOARCH=amd64 go build -o /tmp/grove-reconciliation-linux ./cmd/grove`.
- [x] Compare every spec requirement with Tasks 1–5's tests, inspect changed code for forbidden mutation paths and sensitive output, and verify no unfinished placeholders. Address valid gaps with failing regression tests before fixes.
- [x] Update STATUS and roadmap with implemented behavior, verification and limitations. Mark the decision accepted/implemented only for delivered behavior; preserve indefinite blocking and deferred work explicitly.
- [x] Commit the verification/status update. Use the selected execution workflow's independent review when authorized; assess findings against spec and rerun only affected checks plus mandatory checks after Go fixes.
- [ ] If opening/updating a non-draft PR is authorized, follow AGENTS.md: wait for latest CI and CodeRabbit, retrieve summary/comments/threads, fix actionable feedback and repeat. Do not post bot commands/replies, resolve threads or merge without appropriate authorization. Report skipped/stalled review explicitly and record follow-up in STATUS.

## Execution handoff

The user approved native execution on `feat/operator-reconciliation`. Tasks 1–6
are implemented, reviewed and verified. Independent review of `e860420` reported
five important findings, all fixed in `6d89055`; targeted regression tests and
the full required checks passed afterward. No live run or external PR action was
performed. Hosted CI and CodeRabbit feedback remain unavailable until a PR is
opened.

### Execution record and rulings

- Used the requested feature branch in the existing checkout instead of creating
  a second worktree. This provides branch isolation; it does not isolate the
  working directory from other local tasks.
- Corrected journal validation to accept an empty prior baseline for create
  operations and the verified create result. Malformed states remain covered by
  validation tests; a validator regression could otherwise accept invalid
  create state.
- Preserved joined close failures when presenting safe recovery errors so callers
  can still identify resource failures. `errors.Is`/`errors.As` coverage verifies
  identity; losing it could misclassify a retry as completed.
- Retained selected conflict evidence in its upload journal, atomically retired
  the original conflict record with baseline/candidate commit, and left owned
  stage deletion restartable. Durable-phase and cleanup tests cover the added
  journal coupling; invalid evidence could otherwise hide conflicts or strand
  recoverable stages.
- Validate timestamps on both history anchors and update older success fixtures
  to include complete metadata. This can block recovery when provider metadata is
  incomplete; valid and invalid history cases are tested.
- Recheck the recorded head after competitor capture and preserve provider
  identity for history/capture errors. Without these checks, a stale conflict
  could be committed or a transient provider cause could be lost.
- Live Drive behavior and Linux runtime behavior were not exercised. A provider
  or platform defect may remain; the Linux build is compilation evidence only.
- Earlier-release migration support was excluded because Grove is pre-release
  with zero deployed users. Older persisted states are not promised to migrate.
- Force-abandon, multiple-competitor selection, and unowned artifact
  reconciliation remain deferred. Ambiguous transactions can remain blocked;
  verified exports preserve bytes but do not make another write safe.
- No stronger proof of upload authorship was added beyond the accepted revision
  model. A same-content/different-version outcome can be accepted, while an
  uncertain request may still need operator judgment.
- The independent reviewer declined judgment on live Drive/Linux runtime,
  migrations, deferred recovery modes, and stronger authorship proof for the
  scope reasons recorded above. No minor findings were deferred.
- CodeRabbit's PR #8 security architecture review identified a possible
  temporary-path replacement immediately before export publication. A new test
  reproduced substituted bytes; publication now links from an open handle to a
  private staging directory. Linux and macOS use the existing Unix boundary;
  other platforms return the existing unsupported-publication error. The cost if
  this mitigation is wrong is an export containing unverified bytes, so the
  regression verifies the actual published contents. Fresh hosted checks and
  CodeRabbit review are required after the fix is pushed.
