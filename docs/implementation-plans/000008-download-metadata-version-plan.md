# Download Metadata-Version Reliability Implementation Plan

> **For agentic workers:** Use `superpowers:executing-plans` for native execution or `superpowers:subagent-driven-development` if the user selects delegation. Implement task-by-task and track steps with checkboxes.

**Goal:** Accept proven metadata-only Drive version increases during downloads and their recovery without accepting content or structural changes.

**Architecture:** Add one pure file-metadata comparison used by transfer verification and download recovery. Keep saved operation snapshots unchanged, retain existing artifact/hash checks, and let subsequent scans reconcile baseline versions through metadata-record operations. Do not change the general remote check used for moves and Trash.

**Tech Stack:** Go 1.27.2, existing Drive API interfaces, rooted filesystem operations, private JSON journals, deterministic fake providers and HTTP fixtures.

**Spec:** [000008-download-metadata-version-design.md](../spec/000008-download-metadata-version-design.md), approved 2026-10-22.

**Status:** Approved; native execution in progress. Branch: `fix/download-metadata-version`.

## Global Constraints

- No new CLI flags, remote writes, revision pins, state formats, daemon work, global retry policy, or generalized fingerprint framework.
- Move, upload, and Trash preconditions retain their existing rules.
- Require matching supported file identity/type, one parent, ownership/location, active status, capability, present size, and valid MD5 at every affected comparison.
- Require matching revision/time fields whenever either observation contains them. A changed version requires matching nonempty revision, valid identical RFC 3339 time, and a strictly greater positive decimal int64 version.
- Files without revision/time evidence can still pass the strict equal-version path; this is not permission to accept their version changes.
- Retain the planned remote snapshot in pending records, replacement operations, their verified results, and the completed baseline.
- Preserve actual-byte size/MD5/SHA-256 checks, exclusive artifacts, ownership evidence, no-replace publication, profile/destination/token locking, and interruption recovery.
- Keep raw remote IDs, revision IDs, versions, timestamps, credentials, and file contents out of diagnostics and Git.
- Live tests remain explicitly enabled with the dedicated token, disposable local directories, and fixture-owned remote data. A focused retry cannot turn a failed aggregate run into a passing acceptance result.
- No dependency installation or Go changes are needed while reviewing this plan. At execution, read `AGENTS.md`, the spec, current status/roadmap, and inspect the working tree first.

## Review Focus

- Versions `9` and `10`, integer overflow, signs, and whitespace: compare numeric decimal values safely; test in Task 1.
- Revision or modified-time fields appear/disappear, or two revisions have identical size/MD5: reject lost or changed evidence; test in Tasks 1 and 2.
- An ancestor moves while the file's immediate parent remains unchanged: reject a pending initial file at the new relative path; test in Task 2.
- Disk state is reloaded after a failed save while Drive advances version again: preserve the original snapshot, validate artifacts, and resume safely; test in Tasks 2 and 3.
- Cloud restoration and old records with absent revision/time data: restoration uses ordinary validation, and incomplete evidence never enables the exception; test in Tasks 2 and 4.

## File Map

- Create `internal/syncengine/download_remote.go`: pure comparison and a narrow executor Get/compare wrapper, including categorized sanitized errors.
- Modify `internal/syncengine/transfer.go`: use the comparison after byte verification.
- Modify `internal/syncengine/recover.go`: compare pending files using their full saved `Entry`; retain directory recovery.
- Modify `internal/syncengine/replace.go`: use the download-specific wrapper before unpublished replacement publication.
- Create focused `download_remote_test.go` and `download_recovery_test.go`; extend existing `execute_test.go`, `replace_test.go`, `service_test.go`, and `conflict_service_test.go`.
- Update `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/TESTBED.md`, `docs/STATUS.md`, the roadmap, decision record, spec status, and this plan as work completes.

No changes to `drive.API`, persisted types, `journal.go`'s `checkCurrentRemote`, or the shared planner are expected. Investigate before expanding scope if testing contradicts that expectation.

### Task 1: Verify transfers with content revision and structure

**Files:** Create `internal/syncengine/download_remote.go` and `download_remote_test.go`; modify `transfer.go` and `execute_test.go`.

**Interfaces:**
- `validateDownloadRemote(expected, current drive.File) error`: pure comparison. A mismatch wraps `errPendingRemoteChanged` and reports category booleans, never field values.
- `(*executor).currentDownloadRemote(ctx context.Context, entry Entry) error`: Get the saved ID and call the comparison; retain API errors and add the replacement-recovery boundary to comparison errors.
- `stageDownload(ctx context.Context, entry Entry, path string) (string, error)` keeps its signature, byte hashing, sync/close behavior, and planned snapshot.

- [x] Add `TestDownloadMetadataVersionAdvance`: complete file metadata, version `9` before download and `10` afterward, same revision/time/bytes. Assert one verified download, exact expected local bytes, and completed baseline still recording version `9`. Include a zero-byte subtest. Run `mise exec -- go test ./internal/syncengine -run '^TestDownloadMetadataVersionAdvance$' -count=1`; confirm the existing version comparison causes the failure.
- [x] Add `TestValidateDownloadRemote` with table cases: equal version with complete or jointly absent revision/time evidence; `9` → `10`; `10` → `9`; zero/negative/signed/whitespace/overflow changed versions; missing versions; missing/appearing/disappearing revision/time; malformed relaxed-path timestamp; changed revision with unchanged size/MD5; changed modified time; independent ID/name/MIME/parent/ownership/DriveID/Trash/capability/HasSize/size/MD5 changes. Both observations must be valid; test malformed expected metadata as well as current metadata. Assert errors with `errors.Is(err, errPendingRemoteChanged)`.
- [x] Implement `validateDownloadRemote` with named comparison categories. Require nonempty ordinary binary MIME metadata and reject Google-native types. For differing versions, check ASCII decimal digits before `strconv.ParseInt(..., 10, 64)`, require positive values and `current > expected`, and parse both equal nonempty timestamps using `time.RFC3339Nano`. Equal versions require nonempty version plus all remaining comparison checks; they do not require numeric parsing or nonempty revision/time evidence.
- [x] Replace the inline comparison in `stageDownload` with the helper and wrap comparison errors with the transfer boundary. Implement `currentDownloadRemote` in the same new file; do not alter `currentRemote` or `currentUploadRemote`.
- [x] Add `TestDownloadMetadataDiagnostics`: use distinctive synthetic field values and assert the error exposes category results and the transfer boundary, contains no raw field values, and retains the mismatch sentinel. Keep checksum failure, API failure, cancellation, and no-replace race assertions; add a post-download Get failure and assert no final target is published.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'Test(ValidateDownloadRemote|DownloadMetadata|DownloadVerified|ChecksumMismatch|RemoteChangedDuringDownload|RemoteMovedDuringDownload|Cancellation|NoReplaceRace)' -count=1`. Expected: all selected tests pass, including the existing missing-evidence version-change rejection. Commit as `fix: validate downloaded content across metadata version changes` with a status update and these task checkboxes.

### Task 2: Resume pending initial downloads using the same evidence

**Files:** Modify `internal/syncengine/recover.go`; create `download_recovery_test.go`; extend `execute_test.go` only for shared helpers if needed.

**Interfaces:** Consume `validateDownloadRemote(expected, current drive.File) error`. Keep `recover(ctx context.Context, snapshot Snapshot) error`, `Pending`, `Completed`, `finish`, and `removeTemp` unchanged.

- [x] Add `TestInitialDownloadRecoveryMetadataVersion`: use the existing executor/save-fault pattern from `TestRecoveryBoundaries`, with complete saved file metadata and numeric version advances. Cover durable intent before creation, owned downloading stage, verified stage before publication, and published verified file before baseline save. Reload durable state from disk through `loadState`, rather than relying only on the in-memory copy. Assert version-only changes permit recovery; verified final adoption does not redownload; unverified owned stages may be removed and redownloaded; the original snapshot remains saved. Run `mise exec -- go test ./internal/syncengine -run '^TestInitialDownloadRecoveryMetadataVersion$' -count=1`; confirm strict pending comparison fails.
- [x] Add `TestInitialDownloadRecoveryRejectsChangedEvidence`: revision/time/content/parent changes, an ancestor path move with the same immediate parent ID, absent remote identity, absent relaxation evidence, and corrupt/replaced local artifacts. Assert error, no adoption or unrelated cleanup, and retained recovery evidence. Include legacy-shaped equal-version metadata succeeding and changed versions without evidence failing.
- [x] Split only the pending comparison in `recover.go`: require the saved relative path for files, compare `p.Entry.Remote` against the full scanned `current.Remote`, and wrap category failures with the initial-recovery boundary. Keep missing-identity/path failures sanitized. Preserve the existing folder branch and its `same` behavior, then preserve all local phase/type/hash/ownership checks and cleanup order.
- [x] Add a second qualifying version advance on restart and a save failure after validation; assert durable intent remains recoverable without fabricating revision/time fields. Assert `TestDirectoryIntentRecovery` still passes and its folder behavior is unchanged.
- [x] Run `mise exec -- go test ./internal/syncengine -run 'Test(InitialDownloadRecovery|RecoveryBoundaries|DirectoryIntentRecovery)' -count=1`. Expected: all pass. Commit as `fix: recover pending downloads after metadata-only changes` with status and task updates.

### Task 3: Preserve replacement recovery through metadata-only advances

**Files:** Modify `internal/syncengine/replace.go`; extend `replace_test.go` and, for HTTP wiring, `service_test.go`.

**Interfaces:** Consume `(*executor).currentDownloadRemote(ctx context.Context, entry Entry) error`. Keep `replace`, `recoverReplace`, journal phases, `Operation.Before`, `Journal.After`, and baseline commit rules unchanged.

- [ ] Add `TestReplaceRecoveryMetadataVersion` using `TestReplaceRecoveryMatrix` as the filesystem/journal model. Advance version after staging and again after reloading persisted state. Cover verified/unpublished and backed-up/unpublished states. Assert exact new bytes are eventually published, old content survives until durable commit, and no version-only mismatch cancels or strands the operation. Run `mise exec -- go test ./internal/syncengine -run '^TestReplaceRecoveryMetadataVersion$' -count=1`; confirm current strict recovery rejects it.
- [ ] Use `currentDownloadRemote` at the existing remote-check call in `recoverReplace`. Keep `errors.Is(err, errPendingRemoteChanged)` cancellation handling for genuine mismatches and leave move/Trash callers of `currentRemote` untouched. Retain the original operation and verified-result metadata.
- [ ] Extend the matrix with real revision/time/structure changes, missing evidence, API failure, local edits, changed stage/backup bytes or missing temporary-artifact ownership evidence during cleanup, and save failures at verified, backed-up, publication/baseline commit, and committed cleanup boundaries. Reload disk state and assert preservation/cancellation follows the existing phase policy. A metadata advance must never bypass the verified stage or old-target checks.
- [ ] Add `TestDownloadMetadataDoesNotRelaxOtherOperations`: directly exercise the existing strict `currentRemote` on a version-only change and assert its mismatch sentinel. Run the existing move and Trash integration suites plus upload recovery coverage to show their behavior is unchanged; no production changes to those paths.
- [ ] Run `mise exec -- go test ./internal/syncengine -run '(Replace|Stale|Move|Trash|Upload|DownloadMetadataDoesNotRelax)' -count=1`. Expected: all pass, including existing bad-transfer and unexpected-content preservation cases. Commit as `fix: tolerate metadata-only changes during replacement recovery` with status/task updates.

### Task 4: Verify reconciliation, restoration, and full Linux acceptance

**Files:** Extend `internal/syncengine/service_test.go` and `conflict_service_test.go`; update affected docs listed in the File Map. Existing `tests/live` scenarios remain the live acceptance suite.

**Interfaces:** Existing `Service.Run`, HTTP Drive fixture, persisted `State`, planner metadata-record operation, and deletion-conflict cloud restoration APIs. No new production interface is required.

- [ ] Add `TestServiceDownloadMetadataVersionReconciliation`: inject a one-time metadata-only increase at media download through a test-local HTTP handler wrapper. Run initial sync, then sync again against fresh metadata. Assert zero extra media transfers, no remote mutations, unchanged destination bytes/mtime, and the baseline advances from planned version `9` to observed version `10`. Include an incremental replacement subtest with the same post-transfer version advance.
- [ ] Add `TestCloudRestorationDownloadMetadataVersion`: reuse `TestInterruptedCloudRestoreRetainsUnrelatedDeletionConflict` in `conflict_service_test.go`; inject a qualifying version advance during restored-file transfer. Assert independent expected bytes, restoration completes, unrelated tombstones remain preserved, and the next run transfers nothing. Do not add a separate restoration validator or change conflict policy.
- [ ] Run `mise exec -- go test ./internal/syncengine -run 'Test(ServiceDownloadMetadataVersionReconciliation|CloudRestorationDownloadMetadataVersion)' -count=1`; expected all pass. If either exposes missing integration from Tasks 1–3, fix it within the shared download boundary and rerun the affected focused tests.
- [ ] Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`, and `mise exec -- go build ./...` sequentially. Expected: successful exit codes, no lint findings or races. Re-run only affected checks if subsequent edits require it.
- [ ] Run the full live suite on Linux with `GROVE_LIVE_TEST=1`, `GROVE_TEST_TOKEN_FILE="$HOME/.config/grove-test/token.json"`, and `GROVE_TEST_RUNS_DIR="$HOME/.local/state/grove-test/runs"`: `mise run test-live`. Use required filesystem/network access, keep session commands sequential to avoid token-lock contention, and do not print private records. Expected: the entire command exits zero with all scenarios passed and successful fixture-owned cleanup. If it fails, inspect sanitized diagnostics and retain/report the aggregate failure; investigate before repeating. Any retained fixture cleanup must use the ownership-checked CLI.
- [ ] Update product and architecture descriptions with the implemented transfer/recovery rule and strict fallback; record actual full-suite results and remaining limitations in status/testbed docs. Mark the spec/decision implemented only after evidence supports it. Preserve historical failed-run results and distinguish subsequent runs. Run `git diff --check`; expected no whitespace errors.
- [ ] Review the branch diff against every spec criterion, including unchanged move/upload/Trash checks and original journal snapshots. Use the chosen execution workflow's review process, fix valid findings, and verify affected behavior. Commit as `test: verify metadata-only download recovery on Linux` with documentation and task updates. Opening/pushing a PR is a separate action; if authorized later, follow `AGENTS.md`'s CI and CodeRabbit requirements.

## Handoff

The tasks share one comparison and existing recovery records, so native execution is recommended. Review this plan and select native or subagent-driven execution before implementation. The latest observed aggregate Linux live run remains failed; no implementation or new tests were performed while writing this plan.
