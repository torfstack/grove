# Safe Local Deletion Propagation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Propagate explicit local deletions from a populated profile to Google Drive Trash only when the current remote item or subtree still matches Grove's saved baseline.

**Architecture:** Extend the shared planner to recognize missing tracked local paths only in two-way mode and emit either a typed trash operation or a durable deletion conflict. Execute trash operations through the existing Drive API and profile journal, verify the same remote identity after mutation, then atomically update the baseline. Represent deletion conflicts as tombstones in the existing conflict service and resolve cloud-side choices through normal verified downloads.

**Tech Stack:** Go, existing Google Drive v3 client, syncengine planner/journal/services, Cobra CLI, deterministic fake-Drive integration tests.

**Spec:** [docs/spec/000007-deletion-propagation-design.md](../spec/000007-deletion-propagation-design.md)

Implementation tasks share state and planner interfaces and are delivered in one integrated commit after review fixes.

## Global Constraints

- Keep Linux the primary target and keep platform-specific behavior behind existing boundaries.
- Preserve current-run data safety and interruption recovery; earlier-release migration is not required while Grove is pre-release.
- Only explicit `sync --two-way` may start a new trash operation; require the existing read-write OAuth scope and an established populated profile.
- An incomplete listing, scan error, or malformed state never authorizes deletion.
- Trash only after rechecking the same owned identity, path, parent, type, and saved baseline fingerprint; never permanently delete or empty Trash.
- Folder trash requires complete membership and baseline agreement for every tracked descendant; issue one trash request for the topmost deleted folder.
- An uncertain remote-trash outcome is never retried blindly; preserve the journal until the same identity is confirmed trashed or the state remains blocked.
- Deletion conflicts are tombstones. Existing automatic conflict policies do not select a side; explicit conflict resolution is required.
- Preserve profile, destination, and token-session locking; never allow concurrent writers to one profile.
- One-shot sync detects local changes through its full scan; filesystem watching and event scheduling belong to later `groved` daemon work.
- Never print credentials, operation markers, private stage paths, hashes, or raw Drive IDs.
- Live Drive tests remain explicitly enabled and use dedicated credentials, disposable directories, and fixture-owned remote data.

## Review Focus

- A local folder is absent while a new, changed, moved, or untracked remote descendant exists: preserve the remote subtree and make one durable deletion conflict; cover in Tasks 1 and 3.
- The full listing is incomplete or omits a baseline identity: refuse new trash and preserve local state; cover in Tasks 1 and 2.
- Drive accepts trash but the response or state save fails: confirm the same ID on recovery and commit once, without issuing another request; cover in Task 2.
- A deletion conflict's remote head or subtree changes before resolution: retain the tombstone and content; cover in Task 3.
- An inherited trashed descendant is mistaken for an explicitly trashed folder or an unrelated parent: use Drive's explicit-trash field and exact identity checks; cover in Task 2.

---

### Task 1: Deletion-aware planning and tombstone model

**Files:**
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/compare.go`
- Modify: `internal/syncengine/plan.go`
- Modify: `internal/syncengine/conflict.go`
- Modify: `internal/syncengine/state.go`
- Modify: `internal/syncengine/journal_validate.go`
- Test: `internal/syncengine/plan_test.go`
- Test: `internal/syncengine/conflict_test.go`
- Test: `internal/syncengine/state_test.go`

**Interfaces:**
- Add `OpTrash OperationKind = "trash"` and `Conflict.Deletion bool` plus `Conflict.DeletionSubtree []Completed` to retain the exact baseline entries represented by a local deletion tombstone.
- `BuildPlan` remains strict and download-only. `BuildTwoWayPlan` permits absent baseline local entries, then emits `OpTrash` only when the remote identity/path/type and baseline fingerprint match.
- A topmost missing folder emits one `OpTrash` with `Before` containing the full tracked subtree; descendants are not emitted separately.
- Changed remote items and folders with changed, missing, or additional descendants emit a deletion tombstone conflict. Deletion conflicts always use `ResolutionSide == ""` regardless of `ConflictPolicy`.

- [x] Add planner tests for one unchanged file deletion, an empty folder, a nested folder collapsed to one trash op with sorted subtree baseline, read-only `BuildPlan` still rejecting missing paths, and all remote update/add operations beneath a missing folder being suppressed.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'Test.*(Plan|Conflict|State)' -count=1`; confirm the new deletion-planning assertions fail for the expected missing-path behavior.
- [x] Add planner tests for changed revision/content/path/type, incomplete or missing remote membership, and added/changed descendants beneath a missing folder; assert no `OpTrash` is planned and remote content is represented by one pending tombstone conflict.
- [x] Add service/Scan integration coverage proving an incomplete Drive page aborts before the planner and before any remote mutation; a complete listing missing a baseline ID retains the existing unsupported remote-removal error.
- [x] Implement deletion-subtree validation and topmost-folder collapse in `BuildTwoWayPlan`; preserve `BuildPlan` read-only semantics and keep deletion conflicts out of automatic policy selection.
- [x] Validate tombstone records in state loading: require a safe path, non-empty unique subtree IDs/paths, root entry matching the conflict path and baseline identity, and no content-stage fields for a tombstone.
- [x] Run the focused syncengine test command again; confirm normal upload/download conflict plans remain unchanged.
- [x] Included in the integrated implementation commit `feat: propagate local deletions to Drive Trash`.

### Task 2: Drive trash metadata, execution, and recovery

**Files:**
- Modify: `internal/drive/types.go`
- Modify: `internal/drive/client_test.go`
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/journal_validate.go`
- Modify: `internal/syncengine/execute.go`
- Modify: `internal/syncengine/recover.go`
- Modify: `internal/syncengine/service.go`
- Test: `internal/syncengine/execute_test.go`
- Test: `internal/syncengine/service_test.go`
- Test: `internal/syncengine/recovery_service_test.go`

**Interfaces:**
- Extend `drive.File` and the Drive wire resource with `ExplicitlyTrashed bool`; request `explicitlyTrashed` in metadata fields used by `Get` and `List`.
- Extend `Journal` to accept `OpTrash`, storing the exact `Before` subtree, the target `Entry`, live remote subtree, and phases `intent`, `trash-intent`, and `committed`; trash journals have no filesystem stage artifacts and `After` is empty.
- Add `Result.Trashed int`; increment it only after the verified remote trash and durable baseline commit complete.

- [x] Add Drive HTTP tests proving `Get` and `List` decode `explicitlyTrashed` separately from inherited `trashed`, and the trash method continues to PATCH `trashed:true` rather than calling permanent delete.
- [x] Add executor tests asserting final preflight compares file identity, owned status, parent, name/type, revision/time/size/MD5, and folder membership before exactly one `Trash` call.
- [x] Add executor/recovery tests for confirmed trash, save failure after trash, cancellation, definite pre-send validation failure, uncertain request, changed live metadata, missing identity, and folder descendant state; assert uncertainty never issues a second trash request and baseline removal occurs only after confirmation.
- [x] Implement the trash journal validation and recovery branches. Persist intent before mutation, re-fetch and validate immediately before `Trash`, then re-fetch by recorded ID and require `ExplicitlyTrashed` for a root folder/file and inherited `Trashed` for every recorded descendant before removing the baseline entries.
- [x] Use `applyBaseline` to remove the complete recorded subtree and persist committed state before clearing the journal; retain the journal and original baseline on any ambiguous result.
- [x] Update `service.go` result counting and prove a regular read-only run cannot plan a new trash op while existing journal recovery remains available.
- [x] Run `mise exec -- go test ./internal/drive ./internal/syncengine -run '(Trash|Recovery|Plan)' -count=1`; confirm trash and recovery cases pass.
- [x] Included in the integrated implementation commit `feat: propagate local deletions to Drive Trash`.

### Task 3: Deletion conflict inspection and explicit resolution

**Files:**
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/plan.go`
- Modify: `internal/syncengine/conflict.go`
- Modify: `internal/syncengine/conflict_service.go`
- Modify: `internal/syncengine/execute.go`
- Modify: `internal/cli/conflicts.go`
- Modify: `internal/cli/conflicts_test.go`
- Test: `internal/syncengine/conflict_service_test.go`
- Test: `internal/syncengine/plan_test.go`

**Interfaces:**
- Extend `ConflictCandidateView` with a safe candidate description (`deletion` for a local tombstone); do not represent the absent local bytes as a verified stage.
- `conflicts show` prints a tombstone row for local deletion and the existing safe cloud candidate descriptions; `conflicts resolve <id> --keep local` explicitly selects trash, while `--keep cloud` restores the recorded remote subtree using normal verified download operations.
- Configured `keep-local`/`keep-cloud` policies never auto-resolve deletion conflicts.

- [x] Add service/CLI tests for tombstone detail output with no stage path/hash/raw ID, explicit local resolution, cloud resolution of a file and folder subtree, and automatic policy not selecting either side.
- [x] Add stale-resolution tests where root metadata, descendant membership, or a descendant revision changes after the conflict is recorded; require the conflict and all baseline/local content to remain recoverable.
- [x] Implement tombstone-aware candidate details and resolution validation under existing profile/destination exclusions; reject local resolution for non-deletion conflicts without a valid local stage and keep existing content-conflict selection behavior.
- [x] Implement cloud-side tombstone resolution by atomically dropping only the selected tombstone's tracked baseline subtree and conflict record; the shared planner schedules verified downloads as additions, and the profile remains incomplete until restoration succeeds so download-only sync safely resumes an interrupted restore.
- [x] Implement explicit local-side resolution by routing the selected tombstone through the same trash journal and final preflight as an ordinary local deletion.
- [x] Run `mise exec -- go test ./internal/syncengine ./internal/cli -run '(Conflict|Plan|Sync)' -count=1`; confirm existing multi-candidate and content-conflict selection remains intact.
- [x] Included in the integrated implementation commit `feat: propagate local deletions to Drive Trash`.

### Task 4: CLI output, live acceptance, and documentation

**Files:**
- Modify: `internal/cli/sync.go`
- Modify: `internal/cli/sync_test.go`
- Create: `tests/live/deletion_sync_test.go`
- Modify: `README.md`
- Modify: `docs/spec/000007-deletion-propagation-design.md`
- Modify: `docs/decisions/0006-deletion-propagation-proposal.md`
- Modify: `docs/PRODUCT.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/ROADMAP.md`
- Modify: `docs/STATUS.md`

**Interfaces:**
- Add a `Trashed` count to sync output, distinct from uploads and downloads.
- Extend the opt-in fixture-owned live scenario to delete a file and nested folder locally, verify Drive's trash/inherited-trash metadata, repeat as a no-op, and clean up only fixture-owned data.

- [x] Add CLI output test requiring a separate `Trashed` count and retaining the existing counters.
- [x] Add a fixture-owned fake-HTTP sync integration test for initial population, local file deletion, nested-folder deletion, remote Trash verification, unchanged no-op repeat, and fixture-owned cleanup.
- [x] Extend the explicitly enabled live acceptance case using only dedicated test credentials and a fresh disposable run directory; verify trash state and cleanup without logging credentials or generated Drive IDs.
- [x] Document two-way deletion semantics, Trash behavior, conflicts, safe folder deletion, and unresolved remote-write race limits in README and update the accepted decision record.
- [x] Update status/product/architecture/roadmap with actual implementation and verification; distinguish offline, native, Linux cross-build, Linux live, and macOS live evidence.
- [x] Run focused fixture, CLI, and syncengine integration tests; confirm normal and live-disabled offline paths pass.
- [x] Included in the integrated implementation commit `feat: propagate local deletions to Drive Trash`.

### Task 5: Full verification and branch review

**Files:**
- Review: implementation files and `docs/spec/000007-deletion-propagation-design.md`
- Modify: `docs/STATUS.md`

- [x] Run `mise run fmt` and `git diff --check`.
- [x] Run `mise run lint` and `mise run test`.
- [x] Run `mise run test-race`.
- [x] Run `mise exec -- go build ./...` and `GOOS=linux GOARCH=amd64 mise exec -- go build ./...`.
- [x] Review the branch against each spec rule and Review Focus case; fix Critical or Important findings with failing regression tests first, then rerun affected checks and the full relevant suite.
- [x] Record actual results, live-test limitations, review findings, and remaining risks in `docs/STATUS.md`.
- [x] Included in the integrated implementation commit `feat: propagate local deletions to Drive Trash`.

After local verification, push the custom branch and open a pull request against
`main` as requested. Wait for hosted CI, CodeQL, dependency review, and CodeRabbit
on the latest commit; fix actionable findings and repeat review. Merging remains
a separate explicit user decision.
