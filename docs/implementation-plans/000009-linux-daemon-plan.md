# Minimal Linux Daemon Implementation Plan

> **For agentic workers:** Use `superpowers:executing-plans` for native execution, preserving the user's existing preference. Implement task-by-task with the checkboxes below and one fresh whole-branch review at the end.

**Goal:** Periodically sync one existing Linux profile through a foreground `groved`, using a small config file, safe shutdown, and offline `grove status`.

**Architecture:** Keep sync behavior in `internal/syncengine`; add a locked existing-profile execution/inspection boundary. Put strict config, lifetime ownership, sequential scheduling, and advisory records in `internal/daemon`. Add a non-mutating shared lease probe for status and keep every daemon observation separate from authoritative sync state.

**Tech Stack:** Go 1.27.2, Cobra, existing Drive/OAuth services, standard-library JSON/timers/signals, existing atomic private records and Unix locks. No new dependencies.

**Spec:** [000009-linux-daemon-design.md](../spec/000009-linux-daemon-design.md), approved 2026-10-23.

**Status:** Completed natively on `feat/linux-daemon`; all seven tasks and final verification pass.

## Global Constraints

- One existing profile per process; direct Go service calls, no spawned sync CLI and no second engine.
- Config: absolute `--config` path; private current-user-owned regular nonsymlink UTF-8 JSON; at most 64 KiB; required `version: 1`, absolute `profile_dir`, and explicit `mode: download|two-way`; optional `interval`, default `5m`, minimum `1s`.
- Reject unknown/duplicate fields, wrong types, trailing values, invalid durations, path expansion, and aliases of reserved profile records. Config is read once; restart applies changes.
- No profile creation/rebinding, browser authentication, hot reload, watching, IPC, cron/changes-feed scheduling, systemd installation, or non-Linux daemon runtime.
- Preserve original engine conflict policy, recovery, uncertain-write handling, checksums, journal/artifact ownership, destination/token exclusion, and per-profile writer protection.
- Acquire lifetime daemon ownership before profile/destination/token locks. Startup profile contention is fatal; contention during scheduled attempts records busy and waits normally.
- Immediate first attempt; a full interval after completion; synchronous attempts, no missed-tick queue or outer immediate retry/backoff.
- On SIGINT/SIGTERM, cancel and await the active attempt before releasing lifetime ownership. Daemon-owned status/cleanup failures exit nonzero.
- Status is offline: lease-observed ownership plus historical daemon observations and a consistently locked profile summary. It never authorizes a mutation or treats cached phase/PID as liveness.
- Keep raw errors, credentials, remote IDs, synced filenames/content, and private stage details out of public human/JSON output and logs.
- Keep tools pinned in mise and CI; run appropriate formatting/lint/offline/race/build checks for Go changes. Live runs require explicit enablement, the dedicated token, disposable directories, and fixture-owned data.

## Review Focus

- A valid config's profile disappears or changes binding between attempts: fail before OAuth/Drive, without recreating it; Task 2 tests the same held lock and Task 4 exercises subsequent attempts.
- An exclusive lease is replaced by permission/symlink/inspection errors, or concurrent status readers overlap: report unknown/errors rather than inventing daemon activity; Tasks 1 and 5.
- Cancellation arrives after sync completes but before its status/wait transition, or startup validation returns: start no further work and release ownership only after cleanup; Tasks 4 and 6.
- A status file contains malformed phases/times, private values, or stale next-run information: reject invalid records, label valid history as history, and expose no raw contents; Tasks 4 and 5.
- A two-way attempt encounters an uncertain write or an unresolved deletion tombstone: retain existing engine policy and intent across later scheduled attempts; Tasks 2, 4, and 7.

## File Map and Dependencies

1. `internal/privatefs`: typed busy errors and file-lease acquire/probe primitives.
2. `internal/syncengine`: existing-profile methods, offline summary, and a shared execution body that runs with the caller's profile lock held.
3. `internal/daemon/config*.go`: strict file parsing and Linux ownership validation.
4. `internal/daemon/{types,record,run,clock}.go`: sequential loop and advisory state.
5. `internal/daemon/inspect.go`, `internal/cli/status.go`: offline status and presentation; wire through `Services` and `cmd/grove/services.go`.
6. `cmd/groved`: CLI, Linux signals/platform checks, process tests; update mise's build task so existing CI builds both binaries.
7. `tests/live/daemon_sync_test.go`, project docs: owned live scenarios and acceptance.

Tests live beside their owning components. Tasks 1–3 precede Task 4; Tasks 1, 2, and 4 precede Task 5; Tasks 4–5 precede the binary/process tests. Task 7 integrates all prior work. Keep the public `Service.Run` signature and existing CLI semantics unchanged.

### Task 1: Typed contention and lifetime lease observation

**Files:** Modify `internal/privatefs/lock_unix.go` and `lock_other.go`; create `lock.go` for the shared sentinel and `lease_unix.go`/`lease_other.go` plus `lease_test.go`.

**Interfaces:**
- Export `ErrLocked error`; genuine nonblocking contention from `Acquire`, `AcquireDirectory`, and the new acquisition wraps this sentinel. Ordinary syscall/validation failures must not match it.
- `AcquireLease(path string) (*Lock, error)`: nonblocking exclusive file lease, mode 0600, requiring an existing parent; canonicalize parent aliases and reject a symlink/nonregular leaf. Do not create parent directories or unlink lock files.
- `ProbeLease(path string) (active bool, err error)`: open the same regular leaf read-only without creation, take a shared nonblocking lock, and release it. Missing means false/nil, exclusive contention means true/nil, all other failures mean unknown/error. Shared readers do not block one another.

- [x] Add `TestLeaseExclusiveAndSharedProbe`: hold a real exclusive lease, assert a second acquisition matches `ErrLocked`, and assert probing returns active. Release it and assert inactive; probe a missing path and assert no file/directory appears. Add a subprocess holder using the existing `TestProcessLock` pattern. Run `mise exec -- go test ./internal/privatefs -run 'TestLease' -count=1`; confirm missing lease support fails before implementation.
- [x] Add `TestLeaseProbeReadersAndFailures`: overlapping shared readers must report inactive, not each other as a daemon; permission/open/close failures must remain errors. Reject symlink/directory leaves and support profile-parent aliases. Add `TestLockBusyClassification` for profile/token-style `Acquire` and directory leases, with `errors.Is` assertions distinguishing real contention from malformed paths.
- [x] Implement the primitives using the existing Unix boundary. Resolve the parent rather than following the lock leaf; use regular-file checks and no-follow opens. Keep unsupported-platform stubs and preserve useful error text. Introduce no lock deletion or PID tests.
- [x] Run `mise exec -- go test ./internal/privatefs -count=1` and its race variant. Expected: existing atomic-record/process-lock tests and all new cases pass. Commit `feat: add observable lifetime leases and typed lock contention`, updating status and task progress.

### Task 2: Execute and inspect an existing profile under one lock

**Files:** Create `internal/syncengine/profile.go`/`profile_test.go`; modify `service.go` and `state.go` only to share locked execution and strict existing-state validation.

**Interfaces:**
- `ProfileSummary` has `Binding Binding`, `PopulationComplete bool`, `ConflictCount int`, and `RecoveryPending bool`. The binding is internal service data and must never be directly encoded into public daemon status.
- `(*Service).InspectProfile(ctx context.Context, profileDir string) (ProfileSummary, error)`: offline, require an existing directory/regular state, canonicalize and acquire the profile lock, validate state and its binding, then report summary. Allow valid incomplete profiles; do not use `lockedState`'s populated-profile restriction.
- `(*Service).RunExisting(ctx context.Context, profileDir string, expected Binding, twoWay bool) (Result, error)`: reload existing state, validate canonical binding equals `expected`, and execute while retaining the same profile lock. Missing state never follows `loadState`'s synthetic-new-state path.
- Factor a private `runLocked` execution body from `service.go`, receiving the already validated options/state, registry path, and API opener. Both old `Run` and new `RunExisting` use it; only old `Run` may initialize a profile. Keep lock acquisition outside that body to avoid recursive acquisition.

- [x] Add `TestInspectExistingProfile`: populated/incomplete profiles, conflict count including tombstones, all pending download/transaction/probe shapes, invalid records/binding, missing directory/state, cancellation, and busy profile lock. Assert no OAuth/Drive opening, state mutation, or profile creation. Run the focused profile command and observe the missing existing-profile boundary fail.
- [x] Add `TestRunExistingProfileLockAndBinding`: use `OpenAPI` to try acquiring the same profile lock and prove it is still held. Replace/delete state after startup inspection and assert a missing/rebound/malformed profile fails before `OpenAPI`, does not create a destination/profile, and leaves prior data intact. Include canonical path aliases and a fresh expected-binding comparison for every attempt.
- [x] Extract state validation from decoding as needed, so one locked read is validated rather than a separate existence check followed by a loader that can create synthetic state. Reuse normal `canonicalOptions`/binding and state validation, preserving legacy-incomplete download support. Route both API paths through the shared execution body without changing planner/recovery logic.
- [x] Add `TestRunExistingProfileModesAndRecovery`: download access is read-only, explicit two-way requires populated state and mutation-capable API, pending uncertain writes retain their identity/stages without duplicate mutations, and conflicts keep existing policy. Add deletion/incomplete-restoration coverage; no scheduler-specific recovery bypass.
- [x] Run `mise exec -- go test ./internal/syncengine -count=1` and its race variant. Expected: full engine suite, old CLI-facing service tests, and profile tests pass. Commit `feat: add locked existing-profile sync and inspection` with status/progress.

### Task 3: Load a strict private daemon configuration

**Files:** Create `internal/daemon/config.go`, `config_linux.go`, `config_other.go`, and `config_test.go` with platform-specific tests where needed.

**Interfaces:**
- `type Mode string` with `ModeDownload = "download"` and `ModeTwoWay = "two-way"`.
- `Config` contains canonical `ConfigPath string`, canonical `ProfileDir string`, `Mode Mode`, and parsed `Interval time.Duration`.
- `LoadConfig(path string) (Config, error)` does file/config validation without acquiring any profile lease, writing files, or opening credentials. A private wire type enforces required version/mode/profile fields and duplicate-key detection.
- `ValidateProfileConfig(cfg Config, summary syncengine.ProfileSummary) error` validates config exclusion from the destination/reserved profile files and startup two-way population requirements after locked profile inspection. It has no writes/network side effects.

- [x] Add `TestLoadDaemonConfig`: literals for required fields, explicit modes, `5m` default, `1s` boundary, `999ms`, malformed/overflowing durations, wrong numeric/string types, missing/unsupported versions, unknown/duplicate fields (including escaped duplicate keys), extra JSON values, invalid UTF-8, and the exact 64 KiB limit. Assert accepted config produces the intended mode/duration and rejected input creates no files. Run `mise exec -- go test ./internal/daemon -run 'Test.*Config' -count=1`; observe missing support fail.
- [x] Add Linux file tests for mode 0600, current owner, nonsymlink regular files, group/other permissions, relative config/profile paths, Git ancestors, and literal `~`/environment strings. Verify aliases canonicalize to one profile. Test reserved record aliases, including existing hardlinks, and config inside the destination via `ValidateProfileConfig`. Add populated/incomplete two-way versus download cases.
- [x] Implement bounded reading from the opened file with ownership/type/permission checks on that descriptor; do not stat one object and parse a different reopened object. Use standard-library JSON with unknown/trailing/duplicate checks. Linux ownership validation stays behind a platform boundary; unsupported runtime fails before sync. Errors use fixed reasons/known schema fields and do not echo untrusted values or contents.
- [x] Run `mise exec -- go test ./internal/daemon -run 'Test.*Config' -count=1`. Expected all cases pass; repeat with race detection if helpers involve goroutines. Commit `feat: load private single-profile daemon configuration` with status/progress.

### Task 4: Run sequential attempts with lifetime ownership and durable observations

**Files:** Create `internal/daemon/types.go`, `record.go`, `run.go`, `clock.go`, and focused `run_test.go`/`record_test.go`.

**Interfaces:**
- `Clock` exposes `Now() time.Time` and `Wait(context.Context, time.Duration) error`; production waits use elapsed timers, tests use a controlled clock.
- `Record` contains `Version int`, internal `SessionID string`, `Mode Mode`, `Interval time.Duration`, `Phase string`, `Outcome string`, `UpdatedAt time.Time`, optional attempt-start/finish and next-attempt timestamps, `Counts syncengine.Result`, and a fixed `Reason string`. Use version 1 and the spec's exact phase/outcome vocabulary; no binding or raw error fields.
- `SaveRecord(profileDir string, record Record) error` atomically writes private `daemon-status.json`; `LoadRecord(profileDir string) (*Record, error)` returns nil/nil when absent and validates existing record structure, supported version/enums, required timestamps, and interval. Invalid content yields a fixed unavailable error.
- `Dependencies` injects `InspectProfile func(context.Context,string)(syncengine.ProfileSummary,error)`, `RunExisting func(context.Context,string,syncengine.Binding,bool)(syncengine.Result,error)`, `Clock Clock`, `AcquireLease func(string)(io.Closer,error)`, and `SaveRecord func(string,Record) error`. Production defaults call Tasks 1–2 and the real record writer; the default lease function adapts `*privatefs.Lock` to `io.Closer`.
- `Run(ctx context.Context, configPath string, deps Dependencies) error` loads config once and owns startup, the lease, serial attempts, record transitions, and shutdown. Fatal errors are sanitized; no lower-level error text is logged or stored.

- [x] Add `TestDaemonStartupAndScheduling`: valid real config/profile layout, injected inspection/sync/clock, an immediate attempt, exactly 5m wait from completion, a slow attempt spanning multiple intervals, and no concurrent/catch-up calls. Change config mid-run and assert settings stay fixed until restart. Run `mise exec -- go test ./internal/daemon -run 'TestDaemon' -count=1` and observe the missing scheduler fail.
- [x] Add `TestDaemonStartupOwnership`: missing/malformed profiles, config-destination/reserved aliases, two-way incomplete state, and busy startup profile fail before status/credentials. Only the lifetime/profile guards needed to validate an existing profile may appear. A competing lifetime owner fails before inspection/status/sync. Check real path aliases and fixed lock order, using actual leases as well as injected close-failure cases.
- [x] Implement startup: load config; check existing profile directory/regular state; acquire `daemon.lock`; inspect profile; validate config against its binding; retain that expected binding; persist starting state; then enter the serial loop. Recheck cancellation after each startup boundary and immediately before calling the engine.
- [x] Implement sync/wait transitions. Persist syncing before each invocation; pass the retained expected binding and explicit mode to `RunExisting`; save completion/error/busy counts and next-attempt history after it returns; wait one full interval. Classify only `errors.Is(err, privatefs.ErrLocked)` as busy. An ordinary engine error records `sync_failed` and waits; never parse messages, spawn another sync worker, or clear intent. Status writes and daemon-owned lease cleanup failures are fatal.
- [x] Add `TestDaemonFailureAndStop`: ordinary errors/busy wait normally; changing/missing profiles on later attempts never create them; cancellation during startup/wait/sync and immediately after completion starts no extra invocation. Block the fake active attempt during cancellation and prove the lifetime lease remains held until it returns. Assert stopped recording on clean shutdown and nonzero error for owned cleanup failures.
- [x] Add `TestDaemonRecordFailures`: failing initial/pre-sync save prevents the next call; failing post-sync save preserves committed engine work and stops without retry; cancellation/status/close error combinations preserve failures. Validate malformed phase/outcome/times and record redaction. Add a wall-clock-backward-jump case: timestamp ordering is not an invariant, and the scheduler still waits its elapsed interval. The record is advisory, so record-write failure never rolls back or replaces sync state.
- [x] Run `mise exec -- go test ./internal/daemon -count=1` and its race variant. Expected all config/scheduler/record cases pass. Commit `feat: schedule existing profiles with safe daemon lifecycle` with status/progress.

### Task 5: Observe daemon ownership and profile state offline

**Files:** Create `internal/daemon/inspect.go`/`inspect_test.go`, `internal/cli/status.go`/`status_test.go`; modify `internal/cli/root.go` and `cmd/grove/services.go`/`services_test.go`.

**Interfaces:**
- `ProfileView` exposes only population completeness, conflict count, and recovery pending; it never embeds `ProfileSummary.Binding`.
- `Observation` exposes record mode/interval, historical phase/update time, optional attempt/next timestamps, outcome/counts, and fixed reason; exclude session ID and internal record details.
- `StatusReport` has `Owner string` (`active|inactive|unknown`), nullable `LastRecorded *Observation`, `ProfileState string` (`available|busy|unavailable`), and nullable `Profile *ProfileView`. Its JSON tags use explicit snake_case names.
- `Inspect(ctx context.Context, profileDir string, service syncengine.Service) (StatusReport,error)` combines `ProbeLease`, `LoadRecord`, and the offline profile method. Preserve partial public facts on errors; no auth or sync calls. Add `Services.Status func(context.Context,string)(daemon.StatusReport,error)` and inject the production implementation from command wiring.

- [x] Add `TestDaemonInspectOffline`: active/inactive/missing lease, absent record, stale syncing record after crash, permission/probe errors, concurrent status readers, malformed/unsupported records, and busy/invalid/missing profile. Assert missing records are normal, unknown errors are not reported as inactive, unavailable summaries stay nil, and inspection neither opens credentials nor mutates sync state/artifacts. Run the focused inspection tests and observe missing support fail.
- [x] Implement inspection as separate point-in-time observations. Treat record phase/next timestamps as historical even when the lease is active. Keep non-mutating shared probing independent from exclusive profile-summary locking; profile contention yields busy/unknown summary, not an invented zero count. Return an error for inspection I/O, invalid records, or invalid/missing profiles; inactive/busy observations are valid success.
- [x] Add `TestStatusCommand`: required absolute `--profile-dir`, no positional args, `--json`, identical public human/JSON facts, historical labels, and useful partial report on error followed by a nonzero command result. Inject synthetic private IDs/token paths/session IDs/error strings and assert none appear. Assert no config/token flags or network calls are required and help works without services.
- [x] Wire `grove status` through `cli.Services`, adding no dependency from daemon code back into CLI. Avoid directly encoding `Record`, engine summaries, or raw errors. Render fixed reason labels and explicit unknown/unavailable values.
- [x] Run `mise exec -- go test ./internal/daemon ./internal/cli ./cmd/grove -count=1` and its race variant. Expected all existing CLI commands and new status tests pass. Commit `feat: expose offline daemon and profile status` with status/progress.

### Task 6: Launch the Linux binary and verify real-process exclusion/shutdown

**Files:** Create `cmd/groved/main.go`, `command.go`, Linux/non-Linux platform files, and process tests; modify `mise.toml` and `.github/workflows/ci.yml` only where build wiring needs it.

**Interfaces:** `newCommand(run func(context.Context,string) error) *cobra.Command` forwards the required absolute `--config` path and context. The main/platform boundary handles SIGINT/SIGTERM, uses the real daemon defaults, and prints only sanitized fatal reasons with a `groved:` prefix. `mise run build` produces both `bin/grove` and `bin/groved`; existing CI calls that task.

- [x] Add command tests for required config, absolute paths, unknown flags/extra args, credential-free help, and context forwarding. Add Linux subprocess tests using the real daemon and fake HTTP Drive, not a spawned sync CLI; observe the missing command/process behavior fail before implementing it.
- [x] Implement the Linux signal context and command wiring. Unsupported platforms return a fixed error before any sync/OAuth operation. Preserve direct shared-service calls and wait for `Run` to return before exit; do not detach/daemonize, add reload signals, or use `os.Exit` inside the worker loop.
- [x] Add `TestGrovedProcessOwnershipAndSignals`: start one daemon, observe offline status, reject a second daemon via a different config/profile alias, hold a transfer on the fake HTTP server, send SIGINT/SIGTERM, and assert it stops scheduling, cancels, cleans up, and releases ownership only after the active request exits. Force process death once, verify inactive ownership plus stale history, and restart to complete durable recovery.
- [x] Add manual-sync/recovery contention and shared-token subprocess/HTTP tests. Include cancellation during wait, startup validation, and after completion. Check real profile/destination locks and token exclusion rather than only fake call counts; assert no overlapping writes or implicit deletion/conflict resolution.
- [x] Update mise build to build both binaries with the same pinned tools. Keep Linux CI's lint/test/race/build coverage and explicit lint config unchanged. Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`, and `mise run build` sequentially. Expected zero failures/findings/races and two Linux binaries. Commit `feat: add groved Linux entrypoint and process acceptance` with status/progress.

### Task 7: Verify owned live scheduling and document actual behavior

**Files:** Create `tests/live/daemon_sync_test.go`; update `README.md`, `docs/TESTBED.md`, `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `docs/STATUS.md`, decision/spec statuses, and this plan.

- [x] Add a gated Linux-only `TestDaemonPeriodicDownloadLive`: call the existing live config gate before credentials/build/network work, seed an owned fixture, and perform initial one-shot sync in a disposable existing profile. Write a mode-0600 download config outside Git/destination, launch the actual `groved` binary with a short valid interval, observe its first attempt complete, then apply an owned remote fixture mutation. Acquire the fixture's token session with bounded retries only for typed pre-work lock contention; once held, the daemon must report busy rather than overlap it. Never retry a mutation whose outcome is uncertain. Independently verify the subsequent download and a no-op cycle's unchanged bytes/mtimes and operation counts; query `grove status`, stop via SIGTERM, and verify inactive ownership and owned cleanup.
- [x] Add `TestDaemonPeriodicUploadLive`: reuse the fixture/profile lifecycle with explicit two-way config. After initial idle readiness, make one owned local edit, independently read and verify Drive content after a later attempt, verify a no-op cycle, and stop before cleanup. Do not infer successful remote mutation from daemon status alone or add server-conditional-write assumptions.
- [x] Keep process startup/progress/shutdown waits bounded with safe diagnostics. Stop the daemon before fixture cleanup or any separate session command that needs the same token, except the ownership-checked mutation session coordinated above. A failed run retains private records; clean only recorded owned fixtures. Validate the fixture/token gate remains separate from default offline tests. Use the session-authorized dedicated test profile and explicit `GROVE_LIVE_TEST=1` for live acceptance; preserve any existing authorization without asking again.
- [x] Run the two focused live scenarios only when explicitly enabled, then the full enabled `mise run test-live`. Expected the aggregate suite exits zero and all owned fixtures are cleaned. On failure, investigate the named scenario and retain/report the aggregate failure; do not substitute a focused retry for full acceptance.
- [x] Record actual results, runtime/portability limits, mode semantics, restart-only config changes, historical status labeling, and operator actions for conflicts/auth/pending recovery. Provide a private config example and manual foreground lifecycle; mark watching/systemd as later work. Update spec/decision/plan statuses only to the degree implemented and verified.
- [x] Run `git diff --check`, verify doc links, and review the complete branch against every spec criterion. Apply the chosen native workflow's one fresh whole-branch review, assess findings, fix valid blockers with regression tests, and rerun affected checks. Commit `test: verify periodic daemon sync and Linux acceptance`. Opening a PR, posting replies/resolving GitHub threads, and merging follow their separate authorization and AGENTS.md review rules.

## Handoff

All seven tasks are implemented and verified. See [STATUS](../STATUS.md) for
actual offline/race/build/live results and the independent review's cancellation
finding, now fixed with regressions. Integration remains the user's choice;
file watching and systemd installation are subsequent bounded tickets.
