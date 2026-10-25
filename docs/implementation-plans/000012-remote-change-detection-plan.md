# Remote Change Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for native task-by-task execution. The user delegated routine design choices and authorized continued work through review, fixes, CI and merge; no further design handoff is required. Track steps with checkboxes.

**Goal:** Promptly trigger normal daemon sync from opt-in Drive changes-feed checks without changing sync safety or periodic reconciliation.

**Architecture:** A bounded remote hint source checks an ephemeral account cursor. A context-aware gate serializes remote service calls with RunExisting, including cleanup. The existing scheduler coalesces remote/local hints, retains full-scan deadlines and persists historical check observations.

**Tech Stack:** Existing pinned Go/x/sys/Cobra/OAuth/HTTP stack; no new dependencies.

**Spec:** [Authorized spec 000012](../spec/000012-remote-change-detection-design.md). Detailed choices are delegated agent decisions, not individually user-selected settings. Fresh read-only builder check: "nothing important left".

## Global constraints

- Optional duration `remote_poll_interval`: absent disables, minimum 10s, examples 30s; restart required.
- Ten-second context budget for a whole check, including gate wait; interval measured after completion.
- One-second quiet/five-second maximum debounce; preserve existing periodic and failed/busy sync retry deadlines.
- Feed entries are hints only; no event path or deletion authority, no new mutation semantics.
- Read-only per-check API session, locked existing-profile binding validation, no profile creation, no persistent cursor.
- Initialize cursor with a dirty hint so a later normal scan closes startup observation gaps.
- All pages before cursor advance; response cap 1 MiB, token cap 64 KiB, page cap 100; partial changes can request a scan but errors preserve the original cursor.
- No self-contention on token/profile leases. Serialize API work through cleanup, and join all workers before lifetime ownership release.
- Suppress cancellation-only results, preserve genuine read/check/cleanup faults even during signals. Ordinary check failures do not stop periodic sync.
- No tokens/cursors/Drive IDs/response bodies/synced content in logs or status.
- Local verification uses pinned task-equivalent commands directly to avoid recorded host mise cleanup. CI continues fmt/lint/test/race/build checks via repository tasks. Go=/home/david/.local/share/mise/installs/go/1.27.2/bin/go; GOCACHE=/tmp/grove-watch-go-cache; prepend its bin directory to PATH for spawned commands.
- Live tests are explicitly enabled by the user, using the existing dedicated token and fresh disposable fixture-owned runs only.

## Review focus

1. Cursor initialization after startup sync must force a later scan, rather than lose the observation gap: Tasks 3–5.
2. Changed first page followed by a failed/cyclic later page must retain both dirty evidence and original cursor: Tasks 2–4.
3. A long sync or other profile holding the token must not produce self-busy sync or leaked polling work: Tasks 4–5.
4. Last remote outcome must not erase sync counts or claim current health from a historical record: Tasks 1 and 5.
5. Cancellation while a poll/session close fails must preserve genuine errors after source join: Tasks 3–5.

## Files and responsibilities

- Modify `internal/daemon/config.go`, `config_test.go`, `types.go`, `record.go`, `record_test.go`, `inspect.go`, `inspect_test.go`, `internal/cli/status.go`, `status_test.go`: strict config, historical remote fields/validation and formatting.
- Create `internal/drive/changes.go`, `changes_test.go`: narrow feed HTTP interface, bounded decoding and opaque cursor validation.
- Create `internal/syncengine/remote.go`, `remote_test.go`: locked read-only profile service, all-page traversal, typed cleanup failure.
- Modify `internal/daemon/watch.go`; create `remote.go`, `remote_test.go`, `job_gate.go`, `job_gate_test.go`: bounded source mailbox observation, timed poller and context-aware serialized API work.
- Modify `internal/daemon/run.go`, `watch_run.go`, `watch_run_test.go`; create `remote_run_test.go`: optional source lifecycle, gate coordination, historical records, deadline reuse.
- Modify `cmd/groved/process_test.go`; create `remote_process_test.go`: fake feed endpoints and real-process acceptance.
- Create `tests/live/daemon_remote_test.go`; modify `daemon_sync_test.go`, `daemon_watch_test.go`: reuse actual-binary fixture helper for remote download with local watching enabled for later no-op requests; retain remote-only offline process coverage.
- Update README/systemd guide/product/architecture/roadmap/testbed/status/spec/plan; create `docs/decisions/0012-remote-change-hints.md` distinguishing delegated choices. Existing live watcher observation fix/evidence is committed as `3ace79d` on this branch.

## Task 1: Configuration and historical status

**Interfaces:** Add `RemotePollInterval time.Duration` to Config and Record, `RemoteCheckedAt *time.Time` and `RemoteOutcome string` to Record. Public Observation uses interval string (empty when disabled), timestamp pointer and outcome string. JSON keys: remote_poll_interval, remote_checked_at, remote_outcome. Existing version-1 records with absent fields remain valid.

- [x] Write `TestConfigRemotePollInterval`: omitted disables; 10s/30s valid; 9999ms/null/bool/number/empty/duplicate/overflow rejected. Observe failure with focused daemon tests.
- [x] Implement strict field parsing and config allowance without changing interval/watch_local defaults.
- [x] Add `TestRemotePollingRecordValidation` and `TestRemotePollingStatus`: disabled requires zero/nil/empty; enabled requires interval>=10s; never requires nil timestamp; completed/busy/failed require UTC timestamp; reject malformed combinations. Historical JSON/human output includes interval, last check and outcome, never cursor/credentials; source ownership remains separate.
- [x] Implement validation/DTO/formatting. Run daemon/CLI tests (normal and race); expect PASS. Update STATUS and commit.

## Task 2: Drive feed HTTP boundary

**Interfaces:** Define `ChangesPage{NextToken, StartToken string; Changed bool}` and separate `ChangesAPI` with `StartChanges(context.Context)(string,error)` and `ListChanges(context.Context,string)(ChangesPage,error)`. Client implements it without expanding drive.API.

- [x] Write fake HTTP tests `TestStartChanges`, `TestListChanges`, `TestChangesRejectMalformed`, `TestChangesCancellationRedaction`. Assert correct paths/fields/query flags, absent empty pageToken, opaque token escaping, empty/nonempty/removal entries, required nonempty tokens, JSON shape errors, 64KiB token/1MiB body limits, cancellation identity and fixed sanitized errors.
- [x] Run focused tests and observe failure. Implement endpoints with existing safe read requests/retries and redirect refusal. Request spaces=drive, restrictToMyDrive=true, includeRemoved=true, includeItemsFromAllDrives=false, supportsAllDrives=false, pageSize<=1000, fields for tokens/changes(changeType). Do not fetch IDs or paths.
- [x] Check body read/decode/close errors explicitly; join genuine close failures without exposing response bodies or URLs. Page-shape/pagination ownership stays in Task 3.
- [x] Run Drive normal/race suite; expect PASS. Update STATUS and commit.

## Task 3: Locked existing-profile remote checks

**Interfaces:** Add `RemoteChangesResult{Cursor string; Changed bool}` and `Service.CheckRemoteChanges(ctx context.Context,profileDir string,expected Binding,cursor string)(RemoteChangesResult,error)`. Empty cursor initializes and returns Changed=true. Export sentinel `ErrRemoteCheckCleanup`, detectable through fixed-message wrappers preserving joined causes.

- [x] Add `TestRemoteCheckProfileBoundary`: missing/rebound profile refuses before API open, original binding and read-only access passed, state bytes unchanged, profile/token contention retained, no baseline/destination mutation.
- [x] Add `TestRemoteCheckPagination`: complete/empty/multiple pages advance only at terminal cursor; repeated tokens, both/no continuation fields, page cap, malformed/error/cancelled pages retain original cursor; dirty partial results survive errors. Initialization emits dirty. Use real fake-HTTP client integration for at least one complete multi-page scenario.
- [x] Add `TestRemoteCheckCleanup`: API/session and profile-close failure joined with operation error/cancellation is fatal/detectable and sanitized. Add a narrow unexported helper for closing a checked resource so fake close functions test classification without global mutable hooks.
- [x] Observe failing tests. Implement existingProfile lock and expected binding comparison before read-only OpenAPI; type assert ChangesAPI; bound traversal to 100 pages and visited continuation tokens. Return original cursor on every error. Close API then profile lock before return, preserving result. Classify all genuine close failures with the sentinel.
- [x] Run syncengine normal/race suite; expect PASS. Update STATUS and commit.

## Task 4: Polling source and context-aware API gate

**Interfaces:** Define `ChangeSource` (Ready/Take/Close), embed it into existing Watcher (plus Reconcile). Add `RemoteCheckObservation{CheckedAt time.Time; Outcome string}` and optional `WatchBatch.Remote *RemoteCheckObservation`. Mailbox Take clears timestamps/observation but retains sticky fatal Err. Create `jobGate` with context-aware acquire/release. Add `openRemoteSource(ctx context.Context,interval time.Duration,clock TimerClock,check func(context.Context,string)(syncengine.RemoteChangesResult,error)) ChangeSource`.

- [x] Add gate tests proving cancellation-aware wait, no overlapping jobs and release after callback cleanup. Add source tests for immediate first check, whole-check 10s deadline, interval after check completion, cursor initialization dirty hint, empty-page silence, failed/busy retry without cursor advance, partial dirty results and bounded latest observation.
- [x] Add cancellation/late-close regression tests: ordinary parent cancellation exits normally, cleanup sentinel concurrent with signal stays sticky and survives Take after Close, no timer/goroutine/lease leak. Native mailbox tests stay green.
- [x] Observe failures; implement the poller with one worker and TimerClock, context.WithTimeout(10s), and existing mailbox. Ordinary check failures publish failed/busy and retry; cleanup sentinel publishes sticky fatal and stops. Close cancels and joins idempotently.
- [x] Run daemon normal/race suites; expect PASS. Update STATUS and commit.

## Task 5: Integrate remote/local sources and real process acceptance

**Interfaces:** Add `Dependencies.CheckRemoteChanges` matching Task 3 service method. Run enters timed-source scheduler when watch_local or remote_poll_interval is enabled; both disabled retains original path. Use optional native/remote source channels in select; existing Run signature unchanged.

- [x] Add deterministic `TestDaemonRemotePolling` and `TestDaemonRemoteStatus`: initialize source, update remote fields through coordinator only, preserve sync counts/history, persist one coalesced observation, remote hints share debounce/cooldown/periodic deadlines, initialization forces post-cursor scan. Disabled mode never opens native watcher or remote API.
- [x] Add `TestDaemonRemoteGate` and `TestDaemonRemoteShutdown`: gate RunExisting and check calls, bound poll gate wait and classify timeout as busy, no self-busy sync, late source/engine/cleanup faults retained, genuine failures survive signals and lease closes last. Existing native-only tests retain behavior.
- [x] Observe failures; integrate both sources without a second scheduler. A remote check's context includes gate wait; timeout while waiting yields resource busy. Gate release occurs after complete RunExisting/CheckRemoteChanges return. Capture remote observations in coordinator Take and save using the current valid phase. Close/join each source and drain fatal evidence before lifetime release; native Reconcile only runs when native source exists.
- [x] Extend processDrive before its generic Get handlers with synthetic start/changes endpoints backed by its version. Add `TestGrovedRemoteDownload` with remote-only config, 10s checks and 10m periodic sync: wait for post-initialization settled scan, mutate fake remote bytes/version, verify a later download and no-op settling before periodic due. Use bounded 30s process observation windows rather than the older 10s helper for this case.
- [x] Add real process signal/ownership coverage with remote-only and combined sources. Preserve existing forced-death, token and uncertain-journal process tests. Run affected normal/race suites; expect PASS. Update STATUS and commit.

## Task 6: Live acceptance, documentation and final review

**Interfaces:** Add `TestDaemonRemoteChangesLive`; extend shared `testDaemonVariant(t,upload,watching,remote bool)` while existing callers pass remote=false. Remote live mode uses interval=10m, remote_poll_interval=10s and watch_local=true for explicit later metadata-triggered no-op requests. Remote-only behavior is verified in Task 5.

- [x] Write scenario using existing dedicated fixture helpers and actual binaries. Before remote mutation, require completed remote initialization plus a normal attempt started after that check, zero-operation waiting and next_attempt_at comfortably beyond debounce; this excludes pending startup hints as the download trigger. Independently mutate owned remote fixture, without the periodic-only helper's wait-for-sync-busy step. Verify downloaded manifest before the 90s observation window, log latency, then trigger and verify settled no-op cycles, hashes/mtimes, offline remote/local configuration and SIGTERM. Clean all owned fixtures on failure/success without retrying uncertain mutations.
- [x] Run disabled-mode gate check, then explicitly enabled dedicated-account remote and existing watcher scenarios sequentially. Record actual results and any transport/cleanup limitation; never treat SKIP as acceptance.
- [x] Update all operator/product/architecture/roadmap/testbed/status and decision records, including account-wide false positives, opt-in/defaults, ephemeral cursor startup rescan, contention/10s budget, ordinary failure status, invalid cursor restart recovery, historical status and periodic safety net. Keep credentials/IDs/cursors out of records.
- [x] Run pinned fmt/lint/full offline/full race/build commands and both macOS cross-builds; validate links and diff. Update spec/plan completion only with demonstrated results.
- [ ] Perform one fresh whole-change read-only review under native workflow, assess findings and make regression-backed fixes with fresh verification. Record delegated rulings and limitations. Publish under standing authorization; obtain one completed CodeRabbit review, retrieve summary/inline/threads, fix valid findings, wait for fresh CI, and merge without requesting another review unless the user asks.

## Pre-flight and handoff

Interfaces align: Task 1 config/status consumed by 5; Task 2 ChangesAPI consumed
by 3; Task 3 result/cleanup sentinel consumed by 4/5; Task 4 mailbox/gate/source
consumed by 5; Task 5 runtime consumed by 6. Live harness observation fix and
already-approved watcher acceptance remain separate from production behavior.
Tasks 1–5 and dedicated-account remote/watcher live acceptance are complete.
Periodic download/upload live regressions and full pinned verification also pass.
One independent whole-change review and the initial CodeRabbit review at
75892e3 are complete; valid findings are fixed and fresh local verification
passes. Final-head CI and integration are tracked in PR #19 under standing
authorization. Detailed evidence
and rulings are in STATUS and the plan's ignored execution ledger.
