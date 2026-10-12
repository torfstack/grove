# Multiple Conflict Candidates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Capture every possible competing Drive revision from an upload race as a durable conflict and let the operator explicitly choose among the local candidate and preserved cloud revisions.

**Architecture:** Extend the shared sync engine's conflict record and upload journal to represent a set of independently verified cloud candidates. Keep remote-history discovery and capture in the existing journaled executor; expose safe descriptions and explicit selection through the existing conflict service and CLI. Preserve the current single-candidate behavior and transaction/exclusion boundaries.

**Tech Stack:** Go, existing Drive interfaces, private profile records, Cobra CLI, deterministic fake-provider tests.

**Spec:** [docs/spec/000006-multiple-conflict-candidates-design.md](../spec/000006-multiple-conflict-candidates-design.md)

## Global Constraints

- Keep Linux the primary target and keep platform-specific behavior behind existing boundaries.
- Preserve current-run data safety and interruption recovery; earlier-release migration is not required while Grove is pre-release.
- Keep conflict decisions and recovery in the shared sync engine; CLI code formats service results only.
- Never infer that missing or incomplete revision history authorizes dropping a candidate or completing an upload.
- Preserve profile, destination, and token-session locking; never allow concurrent writers to one profile.
- Never print credentials, operation markers, private stage paths, hashes, or raw Drive revision IDs.
- Live Drive tests remain explicitly enabled and use dedicated credentials, disposable directories, and fixture-owned remote data.

## Review Focus

- A candidate vanishes from a later incomplete or changed revision listing: retain the recorded obligation and block unsafe completion; cover in Task 2.
- A stage path is replaced between verification and use: verify identity and content at use and retain the conflict; cover in Tasks 2–3.
- Candidate ordering has equal or missing timestamps: use a deterministic tie-breaker and never imply timestamp order proves user intent; cover in Task 1.
- The remote head changes after candidate capture: refuse resolution while preserving all candidates; cover in Task 3.
- An old single-candidate conflict is opened, listed, and resolved after state upgrade: preserve the current behavior and stage ownership; cover in Tasks 1 and 3.

---

### Task 1: Versioned candidate records

**Files:**
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/state.go`
- Modify: `internal/syncengine/conflict.go`
- Test: `internal/syncengine/conflict_test.go`

**Interfaces:**
- Produce `CloudCandidate` with `RevisionID`, `ModifiedTime`, `MD5`, `Size`, `SHA256`, `StagePath`, and `StageIdentity` fields.
- Extend `Conflict` with `CandidateSetVersion` and `CloudCandidates []CloudCandidate`; retain decoding and validation for existing version 3 single-candidate records.
- Keep the profile state version at 3. Normalize a legacy single cloud candidate in memory for service use, and write the version 1 candidate-set form on the next state save. Preserve the existing conflict ID for a one-candidate record; include the ordered candidate identities in IDs for newly created multi-candidate records.

- [x] Add tests that load version 3 conflicts with a cloud stage, normalize them without changing IDs or stage identities, and round-trip version 1 candidate sets.
- [x] Add validation tests for duplicate revision IDs, invalid timestamps when supplied, invalid hashes/sizes, unsafe or duplicate stage paths, missing stage identities, malformed set versions, and inconsistent legacy fields.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'Test.*(State|Conflict)' -count=1`; confirm the new cases fail before implementation.
- [x] Implement candidate validation and legacy normalization in `state.go` and `conflict.go`; preserve atomic state saves and stage ownership.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'Test.*(State|Conflict)' -count=1`; confirm legacy normalization and malformed-record rejection pass.
- [x] Commit as `feat: model multiple conflict candidates`.

### Task 2: Journal and capture all competing revisions

**Files:**
- Modify: `internal/syncengine/types.go`
- Modify: `internal/syncengine/upload_execute.go`
- Modify: `internal/syncengine/upload_recovery.go`
- Modify: `internal/syncengine/journal_validate.go`
- Modify: `internal/syncengine/resolution_recovery.go`
- Test: `internal/syncengine/upload_execute_test.go`
- Test: `internal/syncengine/recovery_service_test.go`
- Test: `internal/syncengine/recovery_review_test.go`

**Interfaces:**
- Extend `Journal` with a deterministically ordered collection of candidate capture records, each carrying revision metadata, stage path/identity, phase, and verified hashes.
- Produce a helper that selects the complete competitor set from anchored history and a helper that captures or resumes one identified candidate without replacing another candidate's stage.
- Reuse the existing recovery reason `multiple-competitors` only while candidate capture is incomplete; once the full set is verified, retire the upload transaction and publish one conflict atomically.

- [x] Add executor tests for two and many competitors, deterministic ordering, all-candidate pinning and independent byte verification; assert the transaction retires only after the complete candidate set is durable.
- [x] Add recovery tests for interruption after each pin, stage creation, download, and journal save; assert retry retains prior candidate obligations and does not duplicate or mislabel stages.
- [x] Add tests for incomplete history, disappeared recorded revisions, invalid metadata, changed bytes, quota exhaustion, and stage identity substitution; assert blocked recovery preserves every known candidate and pin.
- [x] Run focused upload and recovery tests and confirm the new tests fail before implementation.
- [x] Implement candidate capture in the existing journal phases and validate the collection in `journal_validate.go`; keep the preflight/current-head anchors, timestamp boundary, and conservative pin ownership unchanged.
- [x] Run `mise exec -- go test ./internal/syncengine -run '(Upload|Recovery)' -count=1`; confirm retries resume safely and a complete set becomes one durable conflict.
- [x] Commit as `feat: capture all competing revisions`.

### Task 3: Candidate-aware conflict selection and resolution

**Files:**
- Modify: `internal/syncengine/conflict_service.go`
- Modify: `internal/syncengine/conflict.go`
- Modify: `internal/syncengine/plan.go`
- Modify: `internal/syncengine/execute.go`
- Modify: `internal/syncengine/resolution_recovery.go`
- Test: `internal/syncengine/conflict_service_test.go`
- Test: `internal/syncengine/plan_test.go`
- Test: `internal/syncengine/upload_execute_test.go`

**Interfaces:**
- Produce `ConflictCandidateView` and a conflict-detail service method that returns local/cloud candidate ordinals, timestamps, and sizes without raw revision IDs, hashes, or stage paths.
- Extend `ResolveConflict` to accept `candidate int`; `0` means local only when `side == SideLocal`, and positive ordinals select a cloud candidate. Reject missing/out-of-range ordinals for multi-candidate cloud selection and reject nonzero ordinals for local selection.
- Persist the selected cloud candidate in the conflict record so the existing journaled resolution path can install its verified staged bytes and upload those bytes as the new head when required.

- [x] Add service tests selecting local and each cloud candidate, plus invalid ordinal, single-candidate compatibility, stale local contents, changed remote head, missing/replaced stages, and retention of all unselected stages.
- [x] Add planner/executor tests proving unresolved multi-candidate conflicts never fall through to configured `keep-cloud` or an ordinary overwrite; explicit selection alone authorizes resolution.
- [x] Run focused conflict, plan, and upload tests; confirm the new cases fail before implementation.
- [x] Implement candidate-aware details and selection; revalidate remote identity/ownership/path/type/head and all candidate stages before recording the selection.
- [x] Ensure resolution journaling and cleanup preserve all candidates until the new baseline is committed, then remove only verified conflict-owned stages while retaining Drive revision pins.
- [x] Run `mise exec -- go test ./internal/syncengine -run '(Conflict|Plan|Upload)' -count=1`; confirm existing single-candidate and two-way behavior remains intact.
- [x] Commit as `feat: resolve selected conflict candidates`.

### Task 4: CLI, recovery diagnostics, and user documentation

**Files:**
- Modify: `internal/cli/conflicts.go`
- Modify: `internal/cli/recovery.go`
- Modify: `internal/cli/root.go`
- Create: `internal/cli/conflicts_test.go`
- Modify: `internal/cli/recovery_test.go`
- Modify: `internal/syncengine/recovery_report.go`
- Modify: `internal/syncengine/recovery_service.go`
- Modify: `README.md`
- Modify: `docs/STATUS.md`
- Modify: `docs/decisions/0004-local-upload-proposal.md`

**Interfaces:**
- Add `grove conflicts show <id>` with stable local/cloud candidate ordinals, known timestamps, and sizes.
- Add `--candidate <ordinal>` to `grove conflicts resolve`; preserve `--keep local|cloud` for existing one-cloud-candidate conflicts.
- Extend recovery reports with recorded and verified candidate counts and a safe next action during partial multi-candidate capture.

- [x] Add CLI tests for candidate detail formatting, required/invalid cloud ordinals, local selection, stable candidate order, and absence of revision IDs, hashes, stage paths, and markers from output.
- [x] Add recovery-report tests for partial capture and fully captured conflict states with correct candidate counts/actions.
- [x] Implement CLI wiring through service interfaces only; keep policy decisions and validation in `syncengine`.
- [x] Document list/show/resolve examples, explicit selection, retained pins, and limitations in README and update the accepted decision record with the completed behavior.
- [x] Run focused CLI and recovery tests; confirm existing command output expectations are intentionally updated.
- [x] Commit as `feat: expose conflict candidate selection`.

### Task 5: Full verification and Linux readiness

**Files:**
- Modify: `docs/STATUS.md`
- Review: all implementation files and `docs/spec/000006-multiple-conflict-candidates-design.md`

- [x] Run `mise run fmt` and `git diff --check`.
- [x] Run `mise run lint` and `mise run test`.
- [x] Run `mise run test-race`.
- [x] Run `mise exec -- go build ./...` and `GOOS=linux GOARCH=amd64 mise exec -- go build ./...`.
- [x] Review the full branch against every success criterion and review-focus case; fix valid findings and rerun affected checks.
- [x] Update `docs/STATUS.md` with actual local and hosted verification, live-test status, review findings, and any remaining limitations.
- [x] Commit as `docs: record multiple candidate verification`.

After implementation, push the feature branch and open a pull request against `main` as requested. Wait for CI and CodeRabbit on the latest commit, address actionable findings, and repeat review after fixes. Merging remains a separate explicit user decision.
