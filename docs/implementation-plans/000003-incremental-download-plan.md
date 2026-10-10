# Incremental Download Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for native execution or superpowers:subagent-driven-development if the user selects delegation. Implement task by task; track steps with checkboxes.

**Goal:** Reconcile remote additions, content updates, and safe moves while detecting local changes and preserving recoverable content.

**Architecture:** Extend the existing full-scan, pure-planner, sequential-executor flow. Refactor transfer verification and recovery into focused files; retain one engine and the existing locking and private-record infrastructure.

**Tech Stack:** Go 1.27.2, Cobra, standard-library HTTP and rooted filesystem access; existing mise tooling. Add pinned golang.org/x/sys v0.37.0 only for native no-replace rename primitives.

**Spec:** [Approved spec 000003](../spec/000003-incremental-download-design.md).

**Status:** Approved for native execution; implementation in progress.

## Global constraints

- Linux remains the acceptance target.
- Uploads and remote-deletion propagation belong to later slices.
- Rehash local files on each run; timestamps alone cannot prove a file is unchanged.
- Advance the baseline only after a verified sync operation.
- Preserve immutable profile bindings and destination registrations.
- Retain profile, token, destination, and ancestor exclusion.
- Failed scans cannot initiate new destination mutations.
- An unchanged repeat performs no media downloads or destination writes.
- Reject local conflicts, remote removals, type changes, occupied target paths, cycles, and swaps before new operations.
- No generic workflow framework, second sync engine, or unrelated package moves.
- Live execution requires explicit enablement and fixture-owned disposable data.

## Review focus

1. Remote version changes without changed bytes: advance metadata without downloading; test in Task 2.
2. Folder move combined with descendant edits or moves: preserve identity and order without moving a child twice; test in Tasks 2 and 5.
3. External content appears at a journal-owned path during recovery: preserve it and stop; test in Tasks 4 and 5.
4. Journal save fails after filesystem success: restart must reconcile the durable older journal; test in Tasks 4 and 5.
5. Version-1 pending work or probe cleanup fails during migration: retain readable old state and stop; test in Task 1.

## File responsibilities

- `internal/syncengine/types.go`: typed operations and versioned pending records.
- `state.go`, new `state_test.go`: validation, atomic migration, identity-based baseline updates.
- `plan.go`, new `compare.go`, `plan_test.go`: indexing, local validation, deterministic reconciliation.
- `execute.go`, new `transfer.go`, new `recover.go`: dispatch, shared verified transfers, recovery dispatch.
- New `replace.go`, `replace_test.go`: replacement transitions and crash tests.
- New `move.go`, `move_test.go`: file/folder move transitions and crash tests.
- Existing `publish_unix.go`, `publish_other.go`; new `move_linux.go`, `move_darwin.go`, `move_other.go`, `move_native_test.go`: platform boundary, no-replace rename.
- `destination.go`, `destination_test.go`: capability probing for planned operations.
- `service.go`, new `service_test.go`, `internal/cli/sync.go`, `sync_test.go`: orchestration and user outcomes.
- `internal/drive/types.go`, new `update.go`, `update_test.go`: narrow fixture mutation support, separate from the read-only sync API.
- New `internal/fixture/mutate.go`, `mutate_test.go`: journaled ownership-checked fixture changes.
- New `tests/live/incremental_sync_test.go`: gated dedicated-account acceptance.
- README and existing product, architecture, testbed, roadmap, status, and decision documents: implemented behavior and limitations.

## Operation and journal contract

Keep `BuildPlan(remote Snapshot, local []LocalEntry, state State) (Plan, error)`.
Define `OperationKind` constants `mkdir`, `download`, `replace`, `move`, `record`,
and `skip`. `Operation` carries `Kind`, target `Entry`, and `Before []Completed`.
`Before` is empty for additions, one baseline entry for replacement or metadata
refresh, and the complete tracked subtree for a folder move. A move changes paths
only; descendant content updates are separate operations after the move.

Version 2 adds a typed pending-operation record with the operation, `Phase`,
`TempPath`, `BackupPath`, `VerifiedSHA256`, and `After []Completed`. Preserve the
version-1 pending shape in a distinct compatibility decoder; do not reuse its
phase strings with new meanings. Allow one pending operation at a time.

`applyBaseline(state *State, before, after []Completed) error` validates identities
and replaces/remaps entries deterministically. Save a candidate state first,
then install it in memory; a failed save must not permit later operations to run.
Replacement/move completion records the new baseline with pending phase `committed`.
Cleanup happens while that pending record still identifies artifacts, then a
second atomic save clears pending. No-op plans do not rewrite the baseline.

## Filesystem transitions and restart matrix

Every mutation uses root-confined paths, rejects symlinks, checks recorded content,
and syncs affected parent directories. No rollback overwrites unexpected content.
Successful atomic JSON persistence is required before the next transition.

### Replacement

1. Persist `intent` with old baseline, target version, random exclusive stage and
   backup paths in the file's parent. Target remains old content.
2. Download to stage, verify size/MD5/version, fsync and close. Persist `verified`
   with the new SHA-256 and target baseline. Invalid stage content is never published.
3. Rehash target and require old baseline. Move target to backup without replacement,
   sync parent, persist `backed-up`. Rehash backup before proceeding.
4. Publish stage to absent target using the existing hard-link boundary; sync parent.
   Rehash final content, atomically save updated baseline plus `committed` pending.
5. Verify backup still matches old content, remove backup and any matching stage,
   sync parent, clear pending atomically. Never use recursive cleanup.

| Durable record / observed paths | Restart action |
|---|---|
| intent; target old, stage absent/partial, backup absent | Remove only the recorded regular stage, clear intent; replan/restart download. |
| verified; target old, verified stage, backup absent | Revalidate remote target and local old bytes; resume backup transition. |
| verified or backed-up; target absent, backup old, stage new | Resume publication after verifying both hashes and remote target. |
| verified or backed-up; target new, backup old, stage absent or new | Adopt verified new baseline; commit, then clean artifacts. |
| committed; target new, backup old or absent | Complete hash-checked cleanup and clear pending. |
| any phase; unexpected target, backup, or verified-stage bytes/types | Stop and preserve paths; do not adopt or delete unexpected content. |

An unverified stage is removable only through its durable exclusive intent; a
verified stage must match its recorded hash. If unpublished remote content changes,
stop preserving old backup and stage; do not replace with stale staged content.
Already published verified content can be adopted regardless of a later remote
edit, which the subsequent plan will reconcile. There may be a temporary missing
target between backup and publication; the operation is recoverable, not a claim
of uninterrupted availability or protection from arbitrary concurrent editors.

### Moves

Persist `intent` with complete source baseline and transformed destination
baseline. Validate exact subtree membership and hashes immediately before a folder
move. Perform one native no-replace rename, sync source and destination parents,
then persist new baseline with `committed` pending. Clear pending atomically.

| Observed source / destination | Restart action |
|---|---|
| Source matches old subtree; destination absent | Revalidate remote path and resume rename. |
| Source absent; destination matches transformed subtree | Adopt move, persist baseline, clear pending. |
| Both present, both absent, or unexpected membership/content | Stop and preserve all paths. |

Native implementation opens source/target parent directories through `os.Root`,
checks ancestors for symlinks, and passes those held directory descriptors plus
base names to Linux `renameat2(RENAME_NOREPLACE)` or macOS `renameatx_np(RENAME_EXCL)`.
Do not fall back to check-then-ordinary-rename. Unsupported filesystems/platforms
fail capability preflight for a move plan. Probe using journal-owned disposable
paths; a destination already present must remain intact. Record probe cleanup
ownership before creation and recover it before local scanning.

### Ordering and migration

Validate all conflicts against the original baseline before execution. Construct
a deterministic dependency graph for parent creation, ancestor moves, descendant
moves, and downloads/replacements. Simulate paths after each move; descendants
carried unchanged have no additional move. Tie-break ready operations by path
then kind. Reject cycles, any initially occupied move destination, and same-run
reuse of a vacated tracked path. A folder moved into itself is invalid.

For version 1, complete existing probe/pending recovery with its old semantics,
validate the resulting local tree and planned changes, then write version 2
atomically before new operations. Failure leaves a valid old record or valid new
record, never partial JSON. New profiles start at version 2. Locks cover migration.

## Task 1: State compatibility and focused executor refactoring

**Files:** types.go, state.go, state_test.go, execute.go, transfer.go, recover.go,
execute_test.go in `internal/syncengine/`.

**Interfaces:** Preserve executor `execute(context.Context, Operation) error` and
`recover(context.Context, Snapshot) error`. Produce typed operations/pending
contract above, `applyBaseline(*State, []Completed, []Completed) error`, and
`(*executor).stageDownload(context.Context, Entry, string) (string, error)` which
returns verified SHA-256 after fsync/close and metadata revalidation.

- [x] Write `TestStateV1Migration`, `TestStateRejectsMalformedBaseline`, and
  `TestBaselineUpdatesByIdentity`: completed and pending v1 records load; duplicate
  IDs/paths, invalid hashes, unknown kinds/phases/versions, escaping or colliding
  artifact paths fail; replacement does not append a second identity.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'TestState|TestBaseline' -count=1`; confirm new behavior fails.
- [x] Implement strict version-specific validation, atomic migration helper
  `migrateState(profile string, state State) (State, error)`, and baseline updates.
  Extract transfer verification and old recovery without changing v1 behavior.
- [x] Add `TestV1PendingRecoveryBeforeMigration` and `TestV1ProbeFailurePreservesState`;
  inject old recovery/save failures and assert reloadable state and intact bytes.
- [x] Run `mise exec -- go test -race ./internal/syncengine -count=1`; require PASS,
  including existing transfer/recovery tests. Commit as `refactor: prepare sync state and transfer boundaries`.

## Task 2: Deterministic incremental planning

**Files:** `internal/syncengine/plan.go`, `compare.go`, `plan_test.go`.

**Interfaces:** Consume Task 1 operation contract. Preserve `BuildPlan` signature;
produce stable operations whose `Before` values use simulated paths after moves.

- [x] Write table tests `TestPlanIncremental` asserting exact operations for a
  new folder/file, same-path changed content, file rename, folder move, metadata-only
  version change (`record`, zero download), and move-plus-content-update.
- [x] Write `TestPlanRejectsConflicts` asserting error and empty plan for local
  edit/deletion/addition, remote removal, type change, duplicate identity, swaps,
  cycles, occupied destination, and path reuse; include a simultaneous local and
  remote edit. `TestPlanNestedMoves` asserts ancestor move once, then independently
  relocated child once, with edits after its final path is established.
- [x] Run `mise exec -- go test ./internal/syncengine -run TestPlan -count=1`; confirm failures.
- [x] Implement ID/path indexes and small comparison helpers, validate the whole
  baseline locally first, and build/topologically order operations as specified.
  Same MD5 and size with a newer version produces `record`; preserve local SHA-256.
- [x] Run the same command; require PASS and stable order under permuted inputs.
  Commit as `feat: plan incremental downloads and moves`.

## Task 3: Native move boundary and capability preflight

**Files:** move_linux.go, move_darwin.go, move_other.go, move_native_test.go,
destination.go, destination_test.go in `internal/syncengine/`; go.mod/go.sum.

**Interfaces:** Produce `moveNoReplace(root *os.Root, source, target string) error`
and extend destination probe to consume planned operation kinds. Add pinned
x/sys dependency; do not expose platform calls to planner or CLI.

- [x] Write `TestMoveNoReplace` for file and folder moves, occupied targets,
  symlink ancestors, and source/target on different parents. Occupied target bytes
  and source must survive unchanged. Test real Linux and macOS implementations.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'TestMoveNoReplace|TestProbe' -count=1`; confirm new tests fail.
- [x] Implement held-parent-descriptor native calls and sanitized error handling;
  extend the existing journaled probe to test required rename capability and cleanup.
- [x] Add `TestProbeMoveUnsupported` and `TestProbeMoveRecovery`: unavailable
  primitive prevents tracked mutations; interrupted probe paths are reconciled
  through durable ownership, never a name glob.
- [x] Rerun targeted tests; require PASS. Cross-build Linux and native macOS.
  Commit as `feat: add rooted no-replace move primitives`.

## Task 4: Recoverable replacements

**Files:** `internal/syncengine/replace.go`, `replace_test.go`, execute.go, recover.go.

**Interfaces:** Produce `(*executor).replace(context.Context, Operation) error`
and `(*executor).recoverReplace(context.Context) error`; consume Tasks 1 and 3.

- [x] Write `TestReplaceVerified` asserting old bytes replaced, one baseline per
  remote ID, new SHA-256, and no artifacts. Write `TestReplacePreservesLocalEdit`
  asserting an edit after planning stops replacement without overwriting it.
- [x] Run `mise exec -- go test ./internal/syncengine -run TestReplace -count=1`; confirm failures.
- [x] Implement the replacement transitions and observation-based recovery matrix.
  Keep pending until cleanup is durable; share staged transfer verification.
- [x] Write `TestReplaceRecoveryMatrix`: inject save/mutation/sync failures before
  and after each transition, reload state from disk, restart, and independently
  assert old/new hashes, membership, and retained ownership. Include checksum and
  version mismatch, cancellation, occupied stage/backup, changed backup bytes,
  failed cleanup, and changed remote after publication. Unexpected bytes survive.
- [x] Run `mise exec -- go test -race ./internal/syncengine -run 'TestReplace|TestRecovery' -count=1`; require PASS.
  Commit as `feat: recover incremental file replacements`.

## Task 5: Recoverable file and folder moves

**Files:** `internal/syncengine/move.go`, `move_test.go`, execute.go, recover.go.

**Interfaces:** Produce `(*executor).move(context.Context, Operation) error` and
`(*executor).recoverMove(context.Context) error`; consume baseline and native
move primitives. Reuse local scan/hash logic to validate exact moved membership.

- [x] Write `TestMoveTrackedSubtree`: empty/nested folders and files move with
  their IDs/hashes intact, no media request, and baseline paths remapped once.
  Write `TestMoveSourceChanged` for unexpected/missing/edited descendants and
  `TestMoveDestinationAppeared` for a raced destination; preserve both trees.
- [x] Run `mise exec -- go test ./internal/syncengine -run TestMove -count=1`; confirm failures.
- [x] Implement move transitions and observation-based restart matrix. Baseline
  remapping includes unchanged carried children; unrelated descendant relocations
  and replacements remain later operations from Task 2.
- [x] Write `TestMoveRecoveryMatrix` for intent-save, rename, both parent-sync,
  baseline-save, and pending-clear failures. Reload durable records before restart;
  assert both-present/neither-present/changed-destination stops without deletion.
  Include moved folder with child update and separate child move after restart.
- [x] Run `mise exec -- go test -race ./internal/syncengine -run 'TestMove|TestPlanNestedMoves' -count=1`; require PASS.
  Commit as `feat: recover tracked file and folder moves`.

## Task 6: Service integration and CLI outcomes

**Files:** `internal/syncengine/service.go`, service_test.go, execute.go;
`internal/cli/sync.go`, sync_test.go; `cmd/grove/services_test.go` containing
the existing `TestCLIWorkflowHTTP` fake HTTP integration harness.

**Interfaces:** Keep `Service.Run(context.Context, Options) (Result, error)` and
CLI flags. Extend `Result` with `Updated` and `Moved`; `Downloaded` includes all
media transfers, `Updated` counts replacements, `Moved` counts explicit move
operations. `record` affects no transfer/move counter.

- [x] Write `TestServiceIncrementalHTTP`: initial sync, remote add/update/file
  rename/folder move, independent local verification, then no-op with zero media
  transfers and destination writes. Use the real Drive adapter and fake server.
- [x] Write `TestServicePreflightPreservesDestination` for incomplete pagination,
  unsupported entries, removals, and conflicts; assert no new intent or destination
  changes. Pending recovery is tested separately from new planning.
- [x] Run `mise exec -- go test ./internal/syncengine ./internal/cli ./cmd/grove -count=1`; confirm new tests fail.
- [x] Wire legacy recovery → local validation → plan → migration → capability
  preflight → execution; v2 recovery remains before new planning. Dispatch `record`
  through durable baseline updates; no-op never calls save. Update CLI help and
  count output without printing file names, IDs, tokens, or HTTP response bodies.
- [x] Add `TestIncrementalWriterExclusion` reusing existing process-lock tests;
  retain cross-registry/nested exclusion. Run the same packages with `-race`;
  require PASS. Commit as `feat: integrate incremental sync into grove`.

## Task 7: Owned fixture mutation and acceptance

**Files:** `internal/drive/types.go`, update.go, update_test.go;
`internal/fixture/mutate.go`, mutate_test.go, record.go, inspect.go, cleanup.go;
`tests/live/incremental_sync_test.go` and existing fixture HTTP harness.

**Interfaces:** Add separate `drive.MutationAPI` embedding `API` plus
`Update(context.Context, string, Update, io.Reader) (File, error)`; `Update`
contains name, add/remove parent IDs, MIME type, and explicit content-change flag.
Keep sync dependent only on `drive.API`. Expose
`fixture.ApplyChanges(context.Context, drive.MutationAPI, string, Manifest) error`
for test use, accepting an expected target manifest with retained logical IDs.

- [x] Write `TestUpdateHTTP` for metadata patch, media replacement, parent change,
  sanitized errors, and no blind mutation retry. Verify request shapes against
  official Drive files.update documentation before implementation.
- [x] Write `TestFixtureChangesOwnedOnly`: unknown descendants, parent/ownership
  mismatch, and incomplete listing fail before mutation. Journal each intended
  change, confirmed metadata, and target manifest; keep unchanged ownership tags.
  Additions reuse seed reconciliation, not a new create lifecycle.
- [x] Run `mise exec -- go test ./internal/drive ./internal/fixture -count=1`; confirm failures.
- [x] Implement minimal fixture update support. Ambiguous updates stop with
  retained record; inspection reconciles by owned ID, parents, name, size and hash.
  Cleanup understands pending/confirmed parent changes and new recorded additions;
  never trash unrecorded data. Reject deletion/type-change target manifests.
- [x] Add fake-HTTP fixture change → inspect → sync → independently verify →
  no-op → cleanup coverage, including interrupted mutation reconciliation.
  Run offline packages with `-race`; require PASS.
- [x] Add `TestIncrementalSyncLive` using existing gated config and session helper:
  seed baseline, initial sync, apply additions/updates/file and folder moves,
  inspect, incremental sync, independent target-manifest verification, no-op, cleanup.
  Run `mise run test` to confirm it skips without loading credentials. Execute
  `mise run test-live` only with explicit dedicated-account enablement; otherwise
  record acceptance pending. Commit as `test: verify owned incremental Drive workflow`.

## Task 8: Documentation, verification, and maintainability review

**Files:** README.md; docs/PRODUCT.md, ARCHITECTURE.md, TESTBED.md, ROADMAP.md,
STATUS.md; new `docs/decisions/0003-incremental-download-recovery.md`; this plan.

**Interfaces:** Document actual commands/state compatibility and guarantees from
Tasks 1–7. No new runtime interfaces.

- [x] Update documents to distinguish implemented behavior from future uploads,
  deletions and conflict resolution; document temporary replacement gaps, strict
  conflict handling, profile version upgrade, and native move capability requirements.
- [x] Review touched code for duplicated transfer logic, growing boolean/phase
  dispatch, unnecessary exports, speculative abstractions, and oversized functions.
  Make only focused refactors with existing behavior/recovery tests preserved.
- [x] Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`,
  `mise run build`, `mise exec -- env GOOS=linux GOARCH=amd64 go build -o /tmp/grove-incremental-linux ./cmd/grove`,
  and `git diff --check`. Require successful exits; report live/Linux runtime
  evidence separately. Record exact results and remaining limitations in STATUS.
- [x] Commit as `docs: record incremental sync behavior and validation`.
- [ ] Obtain whole-branch independent review using the selected execution workflow;
  address valid findings with targeted verification. If opening a non-draft PR,
  wait for latest CI and retrieve CodeRabbit summary, inline comments, and threads;
  fix valid findings and repeat per AGENTS.md. Merge requires explicit authorization.

## Handoff

Recommended execution: native. The tasks share journal and planner contracts, and
implementing them in sequence keeps refactoring cohesive. A whole-branch independent
review should focus on the restart matrices and folder-move ordering. Delegated
task-by-task implementation is available if the user prefers intermediate reviews.

Planning verification: self-review for spec coverage, interfaces, migration,
recovery, scope and review-focus tests; no implementation or live tests run yet.

## References

- [Go rooted filesystem API](https://pkg.go.dev/os#Root)
- [Linux rename semantics](https://man7.org/linux/man-pages/man2/rename.2.html)
- [Drive files.update](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/update)
