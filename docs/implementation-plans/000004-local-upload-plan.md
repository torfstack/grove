# Local uploads with shared conflict policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Review state:** The user approved revision-aware conflict recovery after the
conditional-write probe failed. The normal path uploads when the current remote
matches the saved baseline, after pinning its current head revision. A concurrent
write during upload is recovered from Drive revision history when available. This
plan implements [spec 000004](../spec/000004-local-upload-design.md) and records
the agreed model in [decision 0004](../decisions/0004-local-upload-proposal.md).

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
- Before an existing-file update, the remote must match the saved baseline and its current binary revision and modification time must be recorded and pinned.
- Drive updates are unconditional; immediately recover and pin divergent revisions created during an upload.
- Retain the immutable local stage and journal until the new head is verified and all conflict candidates are preserved.
- Use exclusive profile and destination locks and atomic private profile records.
- Never infer that an incomplete remote listing authorizes deletion.
- Preserve ambiguous local or remote artifacts and outcomes for inspection.
- Keep credentials, tokens, generated Drive IDs, session URLs, and synced content out of Git and logs.
- Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`, native build, and Linux build.
- Live Drive tests are opt-in and use the dedicated test profile, disposable local directories, and fixture-owned remote data.

## Review Focus

- A remote edit arriving after preflight may be accepted by Drive; pin and verify its revision or retain the journal and stop.
- A local edit during staging or upload must remain on disk and must not silently become the recorded uploaded baseline.
- An uncertain create response must reconcile only the preallocated ID and matching private operation marker.
- A pending `ask` conflict restarted without `--two-way` must remain pending and cause no remote mutation.
- A conflict decision made against stale local or remote snapshots must be rejected and require a fresh choice.

---

## File Structure

- `internal/drive/types.go`, `client.go`, and new upload files define the sync mutation API, revision operations, and streaming upload transport; `update.go` remains fixture-oriented unless proven primitives are deliberately shared.
- `internal/syncengine/types.go`, `state.go`, and new conflict files define profile policy and pending records; `plan.go` classifies differences in `BuildTwoWayPlan`; new upload execution/recovery files own staging and journal transitions.
- `internal/cli/root.go` and new config/conflict command files expose service operations; `cmd/grove/services.go` wires the read-write authenticated sync service.
- Existing syncengine tests cover planner/state/recovery; `internal/drive` tests cover fake-HTTP protocol; `tests` covers end-to-end local/remote behavior and opt-in live acceptance.
- `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/TESTBED.md`, and `docs/STATUS.md` record shipped behavior and verification.

## Task 1: Establish upload and revision feasibility

**Files:**
- Read: `internal/drive/client.go`, `internal/drive/update.go`, `internal/drive/types.go`
- Modify: `tests/live/upload_conflict_test.go`; add current-head pin coverage in `tests/live/revisions_test.go`
- Update: `docs/spec/000004-local-upload-design.md` if evidence changes scope

**Interfaces:**
- Produces: documented Drive v3 update behavior and revision operations, including head revision pinning, revision listing, pinning, downloading, and cleanup.

- [x] Record that Drive v3 metadata has no HTTP ETag and a stale version-based `If-Match` resumable update returned HTTP 200 and replaced the competing bytes.
- [x] Verify with fixture-owned live data that the overwritten binary revision can be pinned and downloaded.
- [x] Verify with fixture-owned live data that the current head revision can be pinned before an update and unpinned afterward.
- [x] User approved the preflight-check, pre-pin, optimistic upload, and revision-recovery model; pin quota and the purge race remain documented limits.

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

- [x] Implement conflict policy and pending-record types with stable IDs and validation; covered by state and conflict tests.
- [x] Default absent policy to ask and preserve v1/v2 download profiles under current recovery rules.
- [ ] Extend the state model for pinned Drive revision IDs and durable local/cloud candidate stages in Task 5's v3 journal migration.

## Task 3: Classify local additions, edits, and conflicts in the planner

**Files:**
- Modify: `internal/syncengine/plan.go`
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/plan_test.go`
- Modify: `internal/syncengine/local.go` only if the existing local scan needs a narrow result extension

**Interfaces:**
- Consumes: `ConflictPolicy`, `Conflict`, and profile state from Task 2.
- Produces: deterministic `BuildTwoWayPlan(remote Snapshot, local []LocalEntry, state State) (Plan, error)` output containing upload/create operations or pending conflicts; no filesystem or network mutation occurs in planning.

- [x] Add planner cases for baseline-matching local edits, local additions, nested folders, remote divergence, preflight conflicts, same-path independent additions, and unsupported removals/moves.
- [x] Assert ask records an unresolved conflict without an upload operation; keep-local and keep-cloud mark the selected side; unknown policy is rejected.
- [x] Assert independent same-path additions become a conflict even though neither candidate has a baseline.
- [x] Run focused planner tests first (they failed to compile because the new planner API and operation types were absent), implement `BuildTwoWayPlan`, then run the focused and full sync-engine suites.
- [x] Implement typed upload/create/conflict operation variants without changing download-only `BuildPlan` behavior.

## Task 4: Implement guarded Drive create and update uploads

**Files:**
- Modify: `internal/drive/types.go`
- Modify: `internal/drive/client.go`
- Create: `internal/drive/upload.go`
- Create: `internal/drive/upload_test.go`
- Modify: `internal/drive/client_test.go` if shared request primitives require new assertions

**Interfaces:**
- Produces: a production `SyncMutationAPI` separate from the existing fixture `MutationAPI` and read-only `API`, with ID allocation, preallocated-ID create, unconditional resumable update, revision listing, keepForever pin/unpin, and revision download methods.
- Produces: streaming resumable media transfer with bounded request/retry behavior, explicit zero-byte handling, sanitized errors, and no blind mutation retry.

- [x] Write fake-server tests for ID allocation, session initiation, chunk streaming, final metadata, zero-byte upload, cancellation, retryable reads, non-retried writes, preallocated-ID create, revision listing, pin/unpin, and revision download.
- [x] Run focused Drive tests; the new tests initially failed to compile because the sync mutation and revision methods were absent.
- [x] Implement request/response types and transport; session URLs remain private and errors redact response bodies and URLs.
- [x] Verify update is not blindly retried and revision IDs/content metadata are checked before preservation.
- [x] Run `go test ./internal/drive -count=1`; the full Drive package suite passed.

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
- Produces: version-3 state and journal schemas for upload create/update phases, preflight head revision pin, discovered competing revisions, immutable local/cloud stages with ownership identities and hashes, and stage validation on restart.

- [x] Add tests for private staging, immutable source snapshots, ownership persistence failure, version-3 migration, and malformed upload journals.
- [x] Run focused upload/migration tests first; they failed because stage and journal interfaces were missing.
- [x] Implement version-3 state migration, private local stages, durable upload intent, journal validation, and restart verification that preserves pending stages.
- [x] Run focused tests and the full sync-engine suite; existing v1/v2 download recovery remains covered under its original semantics.
- [ ] Keep upload-session reconciliation, remote revision capture, baseline save, and pin cleanup in Task 6, where the service has the read-write mutation API lifecycle.

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
- [ ] Add failure/restart tests for upload session uncertainty, remote verification, changed local bytes, preflight pin failure, competing revision capture and verification, baseline save, and pin cleanup. Never blindly repeat a remote update.
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

- [ ] Extend offline fixture workflow through populate → local addition/edit → explicit two-way sync → independently verify remote metadata and bytes → no-op repeat → preflight conflict and a simulated post-preflight racing revision → assert both candidates persist and policy resolution behaves as specified → fixture-owned cleanup.
- [ ] Add retry/restart interruption coverage for create ID reconciliation, pinned preflight revisions, failed competing-revision capture, pending user decisions, and baseline/journal recovery.
- [ ] Extend live acceptance to pin a current head before update and verify that a stale upload's competing revision can be pinned and independently downloaded.
- [ ] Keep live tests disabled by default; when explicitly enabled, use the dedicated profile/account, disposable directories, and fixture-owned Drive objects.
- [ ] Run focused integration tests, then `mise run fmt`, `mise run lint`, `mise run test`, and `mise run test-race`.
- [ ] Run `mise run build` only if that task is available; otherwise run `mise exec -- go build ./cmd/grove` and cross-build `GOOS=linux GOARCH=amd64 mise exec -- go build ./cmd/grove` from macOS. Record Linux runtime acceptance separately from cross-compilation.
- [ ] Update product, architecture, testbed, and status docs with actual outcomes and commit.

## Execution notes

The live feasibility gate is complete and the recovery model is approved. Existing
remote edits detected before upload follow the configured conflict policy without
uploading. For the common unchanged case, pin the observed head before uploading;
afterward recover any divergent revision created during the request. If recovery
cannot verify both candidates, preserve the journal and block profile writes.
Conflict settings and pending-conflict methods are shared engine behavior; the
desktop UI itself is not part of this slice. A future daemon must consume these
service methods and must never prompt on a terminal.
