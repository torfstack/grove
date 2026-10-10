# Local uploads with shared conflict policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in two-way sync for populated profiles, with safe local uploads and a profile-scoped conflict policy shared by Grove CLI and future daemon/UI clients.

**Architecture:** Extend `internal/syncengine` as the sole owner of planning, conflict policy, profile state and journaled execution. Add guarded streaming writes to `internal/drive`; expose profile policy and pending conflict operations through the syncengine service and CLI. `groved` can consume those service operations when its daemon/UI work is designed.

**Tech Stack:** Go 1.27.2, standard-library HTTP/filesystem APIs, Cobra, existing `internal/privatefs` records and locks, pinned `mise.toml` tooling.

**Spec:** [docs/spec/000004-local-upload-design.md](../spec/000004-local-upload-design.md)

## Global Constraints

- Linux is the primary acceptance target.
- `grove sync` remains download-only unless `--two-way` is provided.
- Only completed initial profiles may enter two-way mode.
- Conflict policy values are `ask` (default), `keep-local`, and `keep-cloud`.
- A missing policy in valid older state defaults to `ask`.
- Never issue a remote write without explicit two-way opt-in and read-write authorization.
- Never retry a create blindly; persist and reuse a Drive-generated ID and operation marker.
- Existing-file updates require a server-enforced condition validated against the saved baseline.
- Use exclusive profile and destination locks and atomic private profile records.
- Never infer that an incomplete remote listing authorizes deletion.
- Preserve ambiguous local or remote artifacts and outcomes for inspection.
- Keep credentials, tokens, generated Drive IDs, session URLs, and synced content out of Git and logs.
- Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`, native build, and Linux build.
- Live Drive tests are opt-in and use the dedicated test profile, disposable local directories, and fixture-owned remote data.

## Review Focus

- A remote edit arriving after preflight must fail the conditional update and preserve the competing bytes.
- A local edit during staging or upload must remain on disk and must not silently become the recorded uploaded baseline.
- An uncertain create response must reconcile only the preallocated ID and matching private operation marker.
- A pending `ask` conflict restarted without `--two-way` must remain pending and cause no remote mutation.
- A conflict decision made against stale local or remote snapshots must be rejected and require a fresh choice.

---

## File Structure

- `internal/drive/types.go`, `client.go`, and new upload files define the guarded mutation API and streaming upload transport; `update.go` remains fixture-oriented unless proven primitives are deliberately shared.
- `internal/syncengine/types.go`, `state.go`, and new conflict files define profile policy and pending records; `plan.go` classifies differences; new upload execution/recovery files own staging and journal transitions.
- `internal/cli/root.go` and new config/conflict command files expose service operations; `cmd/grove/services.go` wires the read-write authenticated sync service.
- Existing syncengine tests cover planner/state/recovery; `internal/drive` tests cover fake-HTTP protocol; `tests` covers end-to-end local/remote behavior and opt-in live acceptance.
- `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/TESTBED.md`, and `docs/STATUS.md` record shipped behavior and verification.

## Task 1: Establish the conditional-write feasibility gate

**Files:**
- Read: `internal/drive/client.go`, `internal/drive/update.go`, `internal/drive/types.go`
- Create or modify only after mechanism selection: `internal/drive/conditional_update_test.go`, `tests/live/upload_conflict_test.go`
- Update: `docs/spec/000004-local-upload-design.md` if evidence changes scope

**Interfaces:**
- Produces: a documented Drive v3 conditional media-update request contract, including the exact condition, resumable session behavior, stale-condition response, and result reconciliation rules.

- [ ] Review current primary Drive API documentation and identify the supported server-enforced precondition for media updates.
- [ ] Define a disposable-account experiment in the existing opt-in live suite: prepare a known file, change its remote version after the client captures the baseline, attempt the conditional update, and independently verify the competing bytes remain.
- [ ] Run the experiment only when the live suite is explicitly enabled with the dedicated profile and fixture-owned data; record sanitized request semantics and outcome, never IDs or tokens.
- [ ] If Drive rejects stale conditions and preserves the competing version, freeze the transport contract for Task 4. If this cannot be demonstrated, stop this plan, revise spec 000004 to additions-only, and obtain approval for the revised spec and plan before continuing.

## Task 2: Add profile policy and pending-conflict records

**Files:**
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/state.go`
- Modify: `internal/syncengine/state_test.go`
- Create: `internal/syncengine/conflict.go`
- Create: `internal/syncengine/conflict_test.go`

**Interfaces:**
- Produces: `type ConflictPolicy string` with `ConflictAsk`, `ConflictKeepLocal`, and `ConflictKeepCloud` constants.
- Produces: `type ConflictSide string` with `SideLocal` and `SideCloud` constants.
- Produces: validated `Conflict` records with stable ID, relative path, baseline, local fingerprint, remote identity/version/fingerprint, and optional selected side.
- Produces: profile-state validation and migration that defaults absent policy to `ConflictAsk` and accepts v1/v2 download profiles under their current recovery rules.

- [ ] Write `TestLoadStateDefaultsConflictPolicyToAsk`, `TestLoadStateRejectsUnknownConflictPolicy`, and `TestLoadStateValidatesPendingConflicts` with exact accepted enum values and safe relative paths.
- [ ] Run `go test ./internal/syncengine -run 'TestLoadState(Default|RejectsUnknown|ValidatesPendingConflicts)' -count=1`; confirm the new expectations fail before implementation.
- [ ] Implement policy and pending-record types, stable ID derivation from immutable conflict identity, and validation without storing file contents or secrets.
- [ ] Write and run `TestMigrateVersion2StatePreservesBaselineAndDefaultsPolicy`; migration must be atomic and only after legacy recovery.
- [ ] Run `go test ./internal/syncengine -count=1` and commit the state change.

## Task 3: Classify local additions, edits, and conflicts in the planner

**Files:**
- Modify: `internal/syncengine/plan.go`
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/plan_test.go`
- Modify: `internal/syncengine/local.go` only if the existing local scan needs a narrow result extension

**Interfaces:**
- Consumes: `ConflictPolicy`, `Conflict`, and profile state from Task 2.
- Produces: deterministic `Plan` output containing upload/create operations or pending conflicts; no filesystem or network mutation occurs in planning.

- [ ] Add planner cases for unchanged files, local-only content edits, local additions, empty/nested folders, local+remote divergence, remote same-byte version advance, same-path independent additions, and strict unsupported removals/moves.
- [ ] Assert `ConflictAsk` creates pending conflicts and emits no automatic write operation; `ConflictKeepLocal` and `ConflictKeepCloud` select the corresponding action only for eligible tracked-file content conflicts.
- [ ] Assert identical bytes on both changed sides still produce a conflict and unsupported structural changes remain hard errors under every policy.
- [ ] Run `go test ./internal/syncengine -run 'TestBuildPlan.*(Upload|Conflict|LocalAddition|EmptyFolder)' -count=1`; verify failure before planner changes, then pass.
- [ ] Implement the smallest typed operation additions and run all planner tests; commit.

## Task 4: Implement guarded Drive create and update uploads

**Files:**
- Modify: `internal/drive/types.go`
- Modify: `internal/drive/client.go`
- Create: `internal/drive/upload.go`
- Create: `internal/drive/upload_test.go`
- Modify: `internal/drive/client_test.go` if shared request primitives require new assertions

**Interfaces:**
- Produces: a production `SyncMutationAPI` separate from the existing fixture `MutationAPI` and read-only `API`, with `GenerateIDs(ctx context.Context, count int) ([]string, error)`, `CreateWithID(ctx context.Context, id string, input Create, content io.Reader) (File, error)`, and `UpdateConditional(ctx context.Context, id string, input Update, condition WriteCondition, content io.Reader) (File, error)`. `WriteCondition` is fixed by Task 1.
- Produces: streaming resumable media transfer with bounded request/retry behavior, explicit zero-byte handling, sanitized errors, and no blind mutation retry.

- [ ] Write fake-server tests for ID allocation, session initiation, chunk streaming, final metadata, zero-byte upload, cancellation, retryable reads, non-retried writes, preallocated-ID create, and stale-condition rejection.
- [ ] Run `go test ./internal/drive -run 'Test(Upload|ConditionalUpdate)' -count=1`; verify each new test fails for the missing contract.
- [ ] Implement request/response types and transport exactly matching Task 1's proven request semantics; keep session URLs private and redact them from returned errors.
- [ ] Verify the test server can assert that update conditions are sent on the correct request phase and that stale responses map to a typed conflict error.
- [ ] Run `go test ./internal/drive -count=1` and commit.

## Task 5: Add local staging and durable upload recovery

**Files:**
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/journal.go`
- Modify: `internal/syncengine/journal_validate.go`
- Create: `internal/syncengine/upload.go`
- Create: `internal/syncengine/upload_recovery.go`
- Create: `internal/syncengine/upload_test.go`
- Modify: `internal/syncengine/state.go`

**Interfaces:**
- Consumes: guarded `drive.SyncMutationAPI` from Task 4 and typed upload operations from Task 3.
- Produces: version-3 journal records for upload create/update phases, immutable private stages with ownership identity and hashes, and restart reconciliation that preserves ambiguity.

- [ ] Add failure-injection tests for stage creation/ownership persistence, journal intent, upload session, uncertain response, remote verification, baseline save, and stage cleanup.
- [ ] Add recovery tests for reused create IDs, operation-marker mismatch, conditional update rejection, changed local bytes, changed remote version, malformed phase, and v1/v2 download recovery before migration.
- [ ] Run `go test ./internal/syncengine -run 'Test(Upload|RecoverUpload|Migrate).*' -count=1`; confirm failures before implementation.
- [ ] Implement stage ownership and upload journal transitions. Persist intent before every remote write; reconcile existing IDs and never allocate a second ID for an uncertain create.
- [ ] Verify uploaded size/MD5 and independently read back SHA-256 with stable remote metadata checks before applying baseline.
- [ ] Run `go test ./internal/syncengine -count=1` and commit.

## Task 6: Integrate policy, sync execution, and profile service operations

**Files:**
- Modify: `internal/syncengine/service.go`
- Modify: `internal/syncengine/execute.go`
- Modify: `internal/syncengine/replace.go`
- Create: `internal/syncengine/conflict_service.go`
- Modify: `internal/syncengine/service_test.go`
- Create: `internal/syncengine/conflict_service_test.go`

**Interfaces:**
- Produces: `Service.SetConflictPolicy(ctx context.Context, opts Options, policy ConflictPolicy) error`.
- Produces: `Service.ListConflicts(ctx context.Context, opts Options) ([]Conflict, error)`.
- Produces: `Service.ResolveConflict(ctx context.Context, opts Options, id string, side ConflictSide) error`.
- Produces: `Options.TwoWay bool` and `Service.OpenAPI func(context.Context, string, string) (drive.API, func() error, error)`, where the final argument is the required `read-only` or `read-write` access mode.
- Produces: two-way execution that requires complete populated state, explicit opt-in, and read-write API access; existing download-only execution remains read-only.

- [ ] Add service tests proving download-only mode never opens a mutation API, new profiles cannot enter two-way mode, read-only authorization cannot issue writes, and option policy routes the expected conflict action.
- [ ] Add durable `ask` conflict tests: first run records pending conflict with no mutation; repeat reports the same conflict; restart without `--two-way` reports pending work without mutation.
- [ ] Add stale resolution tests: alter local bytes or remote version after listing, resolve by ID, and assert the engine rejects the stale choice while preserving both candidates.
- [ ] Implement service methods under the existing profile lock and use normal transactional executor methods for a selected side; re-scan candidate state before journaling resolution.
- [ ] Run `go test ./internal/syncengine -count=1` and commit.

## Task 7: Expose profile configuration and conflict controls in the CLI

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/config.go`
- Create: `internal/cli/conflicts.go`
- Modify: `internal/cli/sync.go`
- Modify: `internal/cli/sync_test.go`
- Create: `internal/cli/conflicts_test.go`
- Modify: `cmd/grove/services.go`
- Modify: `cmd/grove/services_test.go`

**Interfaces:**
- Consumes: Task 6 service methods.
- Produces: `grove sync --two-way`; profile policy setting with `ask|keep-local|keep-cloud`; `grove conflicts list`; and `grove conflicts resolve <id> --keep local|cloud`.

- [ ] Add Cobra tests for valid/invalid policies, required profile/binding flags, `--two-way` gating, pending conflict output, resolution side selection, and sanitized errors.
- [ ] Run `go test ./internal/cli ./cmd/grove -count=1`; confirm tests fail before command wiring, then pass.
- [ ] Wire commands to service operations; keep policy and sync decisions out of Cobra handlers. Open the Drive session read-write only for two-way execution and a selected remote mutation, never for download-only runs.
- [ ] Run `go test ./internal/cli ./cmd/grove -count=1` and commit.

## Task 8: Verify end-to-end uploads and shared daemon-facing contract

**Files:**
- Modify: existing `tests` fake-HTTP integration workflow
- Modify: `tests/live` opt-in acceptance workflow
- Modify: `docs/TESTBED.md`
- No daemon binary exists in the repository; the syncengine service methods from Task 6 are the daemon-facing contract in this slice.

**Interfaces:**
- Consumes: CLI and service operations from Tasks 6–7.
- Produces: independent local/remote verification for upload, conflict-policy, recovery, and no-op scenarios; documented service operations that a future `groved` UI can consume.

- [ ] Extend offline fixture workflow through populate → local addition/edit → explicit two-way sync → independently verify remote metadata and bytes → no-op repeat → conflicting edits for all policies → assert the selected side wins and the `ask` policy preserves both pending candidates → fixture-owned cleanup.
- [ ] Add retry/restart interruption coverage for create ID reconciliation, stale conditional updates, pending user decisions, and baseline/journal recovery.
- [ ] Keep live tests disabled by default; when explicitly enabled, use the dedicated profile/account, disposable directories, and fixture-owned Drive objects.
- [ ] Run focused integration tests, then `mise run fmt`, `mise run lint`, `mise run test`, and `mise run test-race`.
- [ ] Run `mise run build` only if that task is available; otherwise run `mise exec -- go build ./cmd/grove` and cross-build `GOOS=linux GOARCH=amd64 mise exec -- go build ./cmd/grove` from macOS. Record Linux runtime acceptance separately from cross-compilation.
- [ ] Update product, architecture, testbed, and status docs with actual outcomes and commit.

## Execution notes

The conditional-write feasibility gate is first and blocks Tasks 3–8 until it
passes. If it fails, update and re-review the spec and this plan for additions-only
scope. Conflict settings and pending-conflict methods are shared engine behavior;
the desktop UI itself is not part of this slice. A future daemon must consume these
service methods and must never prompt on a terminal.
