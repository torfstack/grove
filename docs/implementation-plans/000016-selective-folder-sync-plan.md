# Selective Folder Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Let a profile select and safely change which Drive folders its shared sync engine synchronizes.

**Architecture:** Persist an explicit identity-based selection and revision with retained baselines. Resolve remote scope and local coverage before planning, and gate each newly selected subtree until recovery-safe reconciliation finishes. The CLI exposes discovery and selection without depending on a desktop framework; both the CLI and daemon consume the same saved state.

**Tech Stack:** Go, existing Drive API, profile state and recovery journals, Cobra CLI.

**Spec:** [000016-selective-folder-sync-design.md](../spec/000016-selective-folder-sync-design.md).

## Global Constraints

- Preserve the safety and recovery requirements in spec 000016; incomplete remote information never authorizes deletion.
- Acquire the daemon lifetime lease before the profile lock when editing; no concurrent profile writers.
- Keep `all` distinct from an empty include set; existing state without selection means `all`.
- Do not add UI, glob patterns, shared-drive support, or automatic local cleanup.
- Live Drive tests require explicit enablement, dedicated credentials and fixture-owned data.
- Run `mise run fmt`, `mise run lint`, `mise run test`, and `mise run test-race`; verify Linux and macOS builds.

## Review Focus

- Daemon startup races with selection edits: prove one side fails busy before any state write (task 2).
- An excluded sibling is unreadable or a symlink: never inspect its contents while validating structural ancestors (task 3).
- A paginated remote listing fails halfway: preserve previous selection and perform no mutation (tasks 2 and 3).
- A selected ancestor moves with excluded local descendants: block the directory move and preserve both paths (task 5).
- A local move into an excluded subtree appears as deletion: require explicit resolution even with automatic conflict policy (task 6).

### Task 1: Durable selection and recovery state

**Files:** Modify `internal/syncengine/types.go`, `state.go`, `journal_validate.go`; test in `state_test.go` and `recovery_review_test.go`.

**Interfaces:** Add `Selection` to `State`, with explicit mode (`all` or `include`), selected folder IDs, revision, and per-subtree pending/active/dormant state. Carry the selection revision in newly written journals. Existing callers of `loadState` and `saveState` retain their signatures.

- [ ] Add failing round-trip and malformed-state tests for `all`, empty include, monotonic revision, subtree states, unknown modes, duplicate IDs, and absent selection in state versions 1–3. Verify old pending journals remain recoverable.
- [ ] Run the affected `go test ./internal/syncengine` tests and confirm the new cases fail.
- [ ] Implement validated state representation and atomic persistence without rewriting unfinished legacy recovery; reject inconsistent subtree revisions and malformed state.
- [ ] Rerun the affected tests and commit the passing state boundary.

### Task 2: Discovery, initial selection, and locked edits

**Files:** Create `internal/syncengine/selection.go` and `selection_test.go`; modify `internal/syncengine/service.go`, `profile.go`, `internal/cli/root.go`, `sync.go`, `cmd/grove/services.go` and corresponding tests.

**Interfaces:** Provide shared service operations to discover folders under an explicit root and token, inspect a profile selection, and replace it with `all` or a list of IDs. Extend `Options` with an optional initial selection; nil means existing default `all`. Wire these through the CLI service boundary.

- [ ] Test complete paginated discovery, ownership and ancestry validation, nested-ID normalization, empty inclusion, initial selection persisted before mutation, and failed validation leaving the old state byte-for-byte unchanged.
- [ ] Test that pending download, probe, transaction, candidate capture, unresolved conflict and incomplete population block edits; selection-root repair alone permits removing that selection. Test lease then profile-lock ordering and a racing daemon start.
- [ ] Run the new tests and confirm failure; implement discovery and selection edits with read-only authenticated sessions, nonblocking locks held across validation and atomic state update. Do not mutate content during an edit.
- [ ] Add CLI discovery, inspect, and edit commands and initial-sync flags; reject incompatible or ambiguous flags. Test command output and argument validation without printing credentials.
- [ ] Run affected engine and CLI tests; commit the passing service and CLI boundary.

### Task 3: Scope-aware remote and local scans

**Files:** Modify `internal/syncengine/scan.go`, `local.go`, `service.go`; test in `plan_test.go`, `remote_test.go`, and `service_test.go`.

**Interfaces:** Extend remote discovery to return verified selected paths and structural ancestors for a selection revision; pass that scope into local scanning and planning. Preserve existing `Scan` behavior for `all`.

- [ ] Test nested selections, empty scope, identity-preserving rename and move, missing/inaccessible/out-of-root selected identities, pagination failure, duplicate path collisions, and unsupported content outside selected subtrees.
- [ ] Test that excluded local siblings are skipped before path validation, stat, open or hashing, including unreadable directories and symlinks; unsafe structural ancestors fail closed.
- [ ] Run tests to confirm failure; implement verified ancestry and complete selected subtree scans, skipping unselected descendants, and scoped local traversal. Unknown or incomplete identity/ancestry must abort planning.
- [ ] Run affected tests and commit the scan boundary.

### Task 4: Population and re-selection reconciliation

**Files:** Modify `internal/syncengine/service.go`, `plan.go`, `execute.go`, `recover.go`, `conflict*.go`, and their tests.

**Interfaces:** Planning consumes the verified scope and per-subtree revision from tasks 1–3; only durably active subtrees permit ordinary two-way mutation. Retain dormant `Completed` baselines across selection changes.

- [ ] Test newly selected and re-selected subtrees with unchanged data, local-only edits, remote-only edits, both sides edited, locally missing files, remote removal, occupied destinations and local additions during exclusion. Assert no silent upload or overwrite of ambiguous local data and restore a missing local file safely.
- [ ] Test interruption before and after each selection/population persistence boundary and while a journal is pending; old journal recovery uses its original revision and no subtree activates prematurely.
- [ ] Run tests to confirm failure; implement journaled reconciliation and explicit conflict decisions, preserving retained baselines and excluded local data. Mark a subtree active only after verified reconciliation is durably saved.
- [ ] Run affected tests and commit the reconciliation behavior.

### Task 5: Scope transitions and safe moves

**Files:** Modify `internal/syncengine/plan.go`, `move.go`, `execute.go`, `conflict*.go`; test in `plan_test.go`, `move_test.go`, and `service_test.go`.

**Interfaces:** Use task 3's verified ancestry and task 4's subtree gates to classify remote moves into and out of selection.

- [ ] Test verified moves out of scope retain local data and baseline; unverified identities fail closed. Test inbound moves and paths occupied by retained data enter reconciliation.
- [ ] Test selected and structural ancestor renames containing excluded or untracked descendants: no whole-directory move carries them, and a resolvable conflict is reported.
- [ ] Run tests to confirm failure; implement identity/ancestry checks and safe move blocking without granting authority over excluded descendants.
- [ ] Run affected tests and commit the move behavior.

### Task 6: Deletion authority and selection repair

**Files:** Modify `internal/syncengine/plan.go`, `trash.go`, `conflict_service.go`, `service.go`; test in `plan_test.go`, `conflict_service_test.go`, and `service_test.go`.

**Interfaces:** Deletion planning consumes active same-revision coverage; explicit selection-root repair is separate from a deletion conflict.

- [ ] Test partial-scope local absence with each conflict policy: no automatic trash; explicit approval requires full remote subtree and baseline revalidation. `all` retains existing behavior.
- [ ] Test that missing explicitly selected roots require restore or selection removal, never trash; folder deletions cannot cover excluded or unverified descendants. Empty scope never implies deletion.
- [ ] Run tests to confirm failure; implement the partial-selection guard, root repair and resolution revalidation, preserving current full-root behavior.
- [ ] Run affected tests and commit the deletion safety behavior.

### Task 7: End-to-end validation and documentation

**Files:** Modify `cmd/groved/process_test.go`, `internal/cli/sync_test.go`, relevant user documentation, `docs/STATUS.md` and `docs/ROADMAP.md`.

- [ ] Add fake-provider process coverage for initial partial sync, stopped-daemon edit, restart, selection persistence, recovery, and identical CLI/daemon scope. Assert excluded data is neither uploaded nor trashed.
- [ ] Run the process tests, formatting, lint, full offline and race suites, Linux and macOS builds, and `git diff --check`. Record actual results and any unavailable platform or live checks.
- [ ] Document discovery, ID selection, daemon stop requirement, re-selection decisions, partial deletion confirmation, and repair. Record completed work and remaining desktop acceptance in `docs/STATUS.md`.
- [ ] Review the complete change against every rule in spec 000016, fix findings with regression coverage, and commit. Follow the repository's PR and CodeRabbit workflow if publication is requested.
