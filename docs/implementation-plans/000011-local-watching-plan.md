# Local Watching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Preserve the user's recorded native execution preference; one fresh whole-change review follows implementation.

**Goal:** Let opt-in Linux local events trigger prompt, debounced normal sync attempts while preserving periodic reconciliation and engine safety.

**Architecture:** A recursive inotify adapter supplies bounded change hints and fatal errors. One scheduler owns all deadlines and starts at most one shared-engine worker; it keeps consuming hints during attempts. The adapter never plans or performs sync mutations.

**Tech Stack:** Go 1.27.2, existing pinned golang.org/x/sys/unix, existing test fixtures and fake Drive server; no new dependency.

**Spec:** [Approved spec 000011](../spec/000011-local-watching-design.md).

## Global Constraints

- Optional strict boolean `watch_local`, default false; version-1 config, restart required.
- Fixed one-second quiet period and five-second maximum debounce window.
- Watch-enabled startup requires an existing destination directory.
- Immediate startup sync; sequential attempts; periodic deadline cannot be postponed by local hints.
- Failed/busy attempts restrict event retries until one configured interval after completion.
- Hints never authorize uploads, deletions, or recovery; existing mode, locks, binding, and engine checks remain authoritative.
- Recursively watch without following symlinks; lost root identity or unrecoverable coverage failure stops visibly.
- Overflow requests complete reconciliation and watch repair; never treat incomplete observation as deletion evidence.
- Preserve cancellation, await cleanup, close/join readers, then release lifetime ownership.
- Keep paths, credentials, contents, and generated Drive IDs out of public errors/status/logs.
- Pre-release: no format migration, but current-run safety and interrupted recovery remain required.
- Run mise fmt/lint/test/test-race/build; live tests require explicit enablement and dedicated fixture-owned data.

## Review Focus

1. A watched subdirectory swapped for a symlink during registration must not expose an outside tree: Task 3 tests descriptor-anchored registration races.
2. Continuous edits and long engine calls must not postpone periodic remote reconciliation: Task 2 tests deadlines crossing an active attempt.
3. Native watch descriptor reuse and stale ignored/move events must not attach hints to unrelated directories: Task 3 injects event-order races and rebuilds coverage conservatively.
4. A local edit arriving while a completed attempt is being acknowledged must survive into the next pending generation: Tasks 2 and 4 test completion-boundary events.
5. Watcher failure concurrent with SIGTERM must preserve cleanup errors and release all ownership only after worker/reader exit: Task 4 tests joined failures and blocked readers.

## Files and responsibilities

- Modify `internal/daemon/config.go`, `config_test.go`: strict opt-in flag.
- Modify `internal/daemon/types.go`, `clock.go`, `run.go`, `run_test.go`: injectable timer/watcher dependencies and lifecycle integration.
- Create `internal/daemon/schedule.go`, `schedule_test.go`: pure deadline and pending-hint state transitions.
- Create `internal/daemon/watch.go`: bounded hint mailbox and watcher contract.
- Create `internal/daemon/watch_linux.go`, `watch_linux_test.go`: recursive inotify reader, anchored traversal, coverage repair, native resource cleanup.
- Create `internal/daemon/watch_other.go`: unavailable native adapter outside Linux, retaining existing platform boundaries.
- Modify `internal/daemon/record.go`, `record_test.go`, `inspect.go`, `inspect_test.go`, `internal/cli/status.go`, `status_test.go`: historical watching flag and effective deadline.
- Create `cmd/groved/watch_process_test.go`; reuse `cmd/groved/process_test.go` helpers and fake Drive server.
- Create `tests/live/daemon_watch_test.go`; reuse dedicated-account fixture, binary build, and cleanup helpers in `tests/live/daemon_sync_test.go`.
- Update `README.md`, `contrib/systemd/README.md`, `docs/PRODUCT.md`, `ARCHITECTURE.md`, `ROADMAP.md`, `TESTBED.md`, `STATUS.md`, paired spec/plan; add `docs/decisions/0011-local-watching.md`.

## Task 1: Add strict configuration and historical status

**Interfaces:** Add `WatchLocal bool` to `Config`, `Record`, and `Observation`; JSON name `watch_local`, always emitted in records/status. Keep record version 1: the field is additive with false as its zero value. `LoadConfig`, `SaveRecord`, `LoadRecord`, `Inspect`, and `humanStatus` retain signatures. Runtime use of the flag follows in Task 4.

- [ ] Add `TestConfigWatchLocal` asserting omitted/false => false, true => true, and null/string/number/array/object/duplicate => error. Run `mise exec -- go test ./internal/daemon -run TestConfigWatchLocal -count=1`; confirm failure before implementation.
- [ ] Extend allowed config fields and strict boolean parsing without weakening existing config validation.
- [ ] Add `TestWatchLocalHistoricalStatus` and CLI cases: true/false survive save/load/Inspect, JSON emits the boolean, human output says `Last recorded local watching: true` or `false`, and absent history remains absent. Keep status token-free and distinguish historical configuration from ownership/health. Run focused tests and confirm failure.
- [ ] Implement record/DTO/format changes. Run `mise exec -- go test ./internal/daemon ./internal/cli -count=1`; expect PASS.
- [ ] Update STATUS with Task 1 results and commit the bounded change on the execution branch.

## Task 2: Define deterministic deadline and coalescing rules

**Interfaces:** Introduce private `schedule` in `schedule.go` with `newSchedule(interval time.Duration) *schedule`, `hint(first, last time.Time)`, `complete(started, finished time.Time, succeeded bool)`, `next(now time.Time) time.Time`, and `begin(now time.Time)`. `begin` consumes only hints observed before that start; hints received during work remain pending. Startup is immediately due. Add `Timer` with `C() <-chan time.Time`, `Stop() bool`; replace `Clock.Wait` with `Clock.NewTimer(time.Duration) Timer`, preserving `Now() time.Time`. Adapt the existing fake clock tests to controllable timers. No wall-clock sleeps in scheduler unit tests.

- [ ] Add table-driven `TestScheduleDebounce`: first hint at t=0 => t=1s; more hints at .8s => 1.8s; continued hints never defer past t=5s. Add `TestSchedulePeriodicFairness`: event attempt before periodic due leaves original due unchanged, simultaneous deadlines combine, and periodic due during work resets to completion+interval.
- [ ] Add `TestScheduleRetryRestriction`: failed/busy completion t=10s with interval 60s prevents event attempt before t=70s, preserves hints, and success removes the restriction. Earlier periodic work is still permitted by its own deadline. Add `TestScheduleHintsDuringAttempt` and `TestScheduleCompletionBoundaryHint` proving one pending follow-up and survival of newly observed generations.
- [ ] Run `mise exec -- go test ./internal/daemon -run TestSchedule -count=1`; confirm new tests fail.
- [ ] Implement pending first/last times and independent periodic/retry deadlines. Debounce deadline is min(last+1s, first+5s); event deadline is max(debounce,retry restriction); overall due is min(periodic,event). Only success/busy/failure completion changes attempt history; elapsed times carry monotonic components, wall timestamps are for display. An attempt crossing the periodic deadline satisfies it even if unsuccessful, matching periodic retries.
- [ ] Implement real timer wrapper and adapt existing controlled-clock tests. No second clock source or duration arithmetic from serialized UTC timestamps.
- [ ] Run `mise exec -- go test ./internal/daemon -count=1` and `mise exec -- go test -race ./internal/daemon -count=1`; expect PASS. Update STATUS and commit.

## Task 3: Implement bounded recursive Linux watching

**Interfaces:** In `watch.go` define `WatchBatch{First, Last time.Time; Err error}` and `Watcher` methods `Ready() <-chan struct{}`, `Take() WatchBatch`, `Reconcile(context.Context) error`, `Close() error`. `Take` atomically drains a bounded mailbox; zero First means no change, Err is sticky terminal failure. Ready is a capacity-one wake channel, not an event log. Add dependency `OpenWatcher func(context.Context, string, Clock) (Watcher, error)`; production factory `openWatcher(ctx context.Context, root string, clock Clock) (Watcher, error)`. Reconcile is serialized with coverage mutation internally; native reads continue while walks execute. Close is idempotent and joins all adapter workers. Internal syscall hooks may be injected only for fault/race testing.

- [ ] Add native tests `TestWatchNestedAndAtomicSave`, `TestWatchMovedInTree`, `TestWatchRemovalAndMetadata`, `TestWatchReadDoesNotNotify`, `TestWatchRootIdentity`, `TestWatchSymlinkRegistrationRace`, `TestWatchOverflowRepair`, `TestWatchDescriptorReuse`, `TestWatchSetupFailure`, and `TestWatchCloseUnblocks`. Assert hints and coverage rather than exact kernel event counts. Use readiness barriers and bounded waits for OS integration, with synthetic overflow/resource errors through narrow hooks.
- [ ] Add mailbox tests proving millions of merged hints retain earliest/latest timestamps with bounded memory, terminal errors survive coalescing, and concurrent Take/notification never loses a wakeup. Run `mise exec -- go test ./internal/daemon -run 'TestWatch' -count=1`; confirm new failures.
- [ ] Implement inotify using existing x/sys and an os.File-compatible nonblocking descriptor with close-unblocks-reader behavior. Subscribe to create/delete/move, write/close-write, relevant attributes, self-delete/self-move, unmount/ignored, and overflow; exclude open/access/close-nowrite. Treat syscall errors with fixed public messages retaining causes internally.
- [ ] Anchor traversal in a retained root directory identity. Register directories before enumerating; use descriptor-relative, no-follow directory opens and register the anchored directory inode (for example its proc-fd handle), never an unchecked concatenated pathname. Check ancestry/root identity around coverage rebuilds; skip vanished children only after reconciling their parent's changed membership. Permission/resource errors are fatal. Unexpected ignored/self-move events invalidate coverage; do not guess mappings from stale descriptors. Verify proc availability as an explicit setup prerequisite if that registration method is used.
- [ ] Keep one native reader draining while coverage changes are serialized by an adapter worker. Coalesce structural rebuild requests; ordinary file hints require no full walk. Mark newly covered subtrees dirty, retain hints arriving during enumeration, and distinguish own removed watches from unexpected coverage loss. On overflow, rebuild the watch set without exposing stale descriptor mappings, preserve a dirty generation, and notify a full-scan hint. Bound transient work by watch set size; inability to restore coverage is fatal.
- [ ] Implement non-Linux unavailable adapter and cleanup that stops workers, closes descriptors, joins readers, and reports real close errors. Run focused native tests and `mise exec -- go test -race ./internal/daemon -count=1`; expect PASS. Update STATUS and commit.

## Task 4: Integrate watcher, scheduler, engine, and shutdown

**Interfaces:** `Run` retains its public signature; consume Task 2 schedule/timers and Task 3 OpenWatcher. Use one buffered engine-result channel containing result/error and start/completion elapsed timestamps, one engine worker maximum, and a derived cancel-cause context. Existing InspectProfile/RunExisting/AcquireLease/SaveRecord dependencies remain. Preserve `Record.Outcome` values and sanitized attempt classification.

- [ ] Add `TestDaemonWatchScheduling` with fake watcher and clock: initialize before startup engine call, watch=false never opens adapter, edits while idle/startup/active attempts trigger debounced work, fake engine concurrency never exceeds one, and missed events are covered by unchanged periodic due times.
- [ ] Add `TestDaemonWatchStatusDeadline`, `TestDaemonWatchRetry`, `TestDaemonWatchFailureDuringSync`, `TestDaemonWatchShutdownJoins`, and `TestDaemonWatchCompletionBoundary`. Assert effective next_attempt_at after coalesced deadline changes, no raw-event status writes, failed/busy cooldown, root/setup failure before mutation, sticky errors, retained last attempt counts, joined genuine cleanup errors, and lease close strictly after worker and reader cleanup. Extend existing signal/cause-loss regression coverage, including cancellation concurrent with watch failure.
- [ ] Run focused daemon tests; confirm new failures. Integrate scheduler into Run without duplicating the existing attempt classification, record writer, or stop handling. Keep the scheduler responsive while one worker calls RunExisting; pass original validated binding and explicit mode on every call.
- [ ] On worker completion, request adapter Reconcile before allowing the next attempt; keep its native reader draining. Take pending batches around reconciliation and completion boundaries so acknowledgement never clears later edits. Combine only pre-start hints into an attempt. Fatal watch failure cancels active work and waits for its result/cleanup, then closes/joins watcher before releasing lease. Preserve fixed watcher error plus genuine joined cleanup failures.
- [ ] Publish waiting history only when entering waiting or the effective coalesced deadline changes. Populate watch_local on every record and clear next_attempt_at when syncing/stopping. Maintain all existing periodic-only startup, record-write-failure, lock, missing-profile, and historical-status tests.
- [ ] Run `mise exec -- go test ./internal/daemon ./internal/cli ./cmd/groved -count=1` and race variants; expect PASS. Update STATUS and commit.

## Task 5: Prove real filesystem and process behavior

**Interfaces:** Reuse `newProcessFixture`, `startDaemonChild`, `waitReady`, `waitProcess`, and fake provider injection in `cmd/groved/process_test.go`. Add private config fixtures with watch_local=true and a long interval so event-triggered work is distinguishable. No production endpoint override is introduced.

- [ ] Add `TestGrovedWatchDownloadSettles`: a fake remote change causes destination writes during startup; wait for the expected no-op follow-up, then prove attempt count remains stable over more than one debounce window and before periodic due. Read/hash scans themselves produce no repeated work.
- [ ] Add `TestGrovedWatchEditDuringSync` using HTTP barriers and an actual subprocess: edit during a blocked transfer, release it, and prove a later engine attempt inspects the edit under existing mode/conflict semantics. Add nested-directory and atomic-save cases. If upload verification needs broader fake mutation support, extend that fixture only with its existing HTTP contract.
- [ ] Add `TestGrovedWatchSignalsAndFailure`: SIGINT/SIGTERM while idle, during active work, and during coverage repair exit cleanly; removed/replaced root exits sanitized and nonzero; a second process can acquire ownership after cleanup. Existing uncertain-journal/stage and token exclusion assertions remain valid.
- [ ] Run new cases before production fixes and retain any reproducer. Run `mise exec -- go test ./cmd/groved -run TestGrovedWatch -count=1` and race variant; expect PASS. Bound waits and always reap child processes and clean fixture-owned local artifacts. Update STATUS and commit.

## Task 6: Add gated live acceptance and operator records

**Interfaces:** Add `TestDaemonLocalWatchUploadLive` in `tests/live/daemon_watch_test.go`, using existing live config gates, actual built binaries, dedicated token, fixture seeding/inspection, independent content verification, and owned cleanup. Config interval is 10m with watch_local=true. Do not change existing periodic live tests into watcher tests.

- [ ] Write the live scenario: establish a populated profile and settled startup/no-op, edit one fixture-owned local file, observe a completed upload and independently verify Drive bytes within a bounded 90s window well before 10m periodic due. Observe the subsequent zero-operation settled attempt; check all file hashes/mtimes remain stable, offline watch_local status, and SIGTERM shutdown. Cleanup must run on failure without blindly retrying uncertain remote writes.
- [ ] Run `mise exec -- go test ./tests/live -run TestDaemonLocalWatchUploadLive -count=1` without live enablement; expect explicit SKIP, no token or network use. Live execution remains pending until explicitly enabled with dedicated token/runs paths; record actual results or this limitation, never equate gated skip with acceptance.
- [ ] Update README/config and systemd guide examples with opt-in flag, one/five-second timing, existing-root prerequisite, cooldown, advisory deadlines, fatal watcher failures, limits on network/unsupported filesystems and proc availability if used. Retain restart-required config behavior and unchanged service settings.
- [ ] Record accepted decision 0011 for native hints plus periodic reconciliation; identify implementation-specific choices as such. Update product/architecture/roadmap/testbed/status, paired spec, and this plan with actual behavior, checks, and remaining limits.
- [ ] Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`, `mise run build`, and `git diff --check`; require success before reporting readiness. Cross-build both binaries for macOS with temporary outputs to ensure Linux-specific APIs stay behind platform boundaries; compilation is not runtime acceptance.
- [ ] Perform one fresh read-only whole-change review under the native execution workflow. Assess findings against approved scope, fix valid issues, rerun affected checks, and record unresolved limits. Commit completed code/docs. Push/PR/comment/merge are not authorized by this plan.

## Self-review and handoff

Spec coverage: configuration/status => Task 1; scheduling and elapsed timing =>
Task 2; recursive coverage, overflow, identity and bounded hints => Task 3;
engine integration, record deadlines and cleanup => Task 4; real filesystem and
process acceptance => Task 5; live harness, docs and final verification => Task 6.
All five Review Focus cases have explicit tests. Interface names are consistent
across tasks; the shared engine's planner/mutation API is unchanged.

Plan is prepared, not executed. Native execution is retained from the existing
project workflow. Review this plan before implementation. At execution time,
start an isolated feature branch/worktree from refreshed main and carry only
these approved design/plan/status changes, preserving the current working tree.
