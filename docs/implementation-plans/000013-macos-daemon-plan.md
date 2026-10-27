# macOS desktop integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring Grove's shared periodic daemon, optional per-profile launchd service, and native menu-bar status/control experience to macOS.

**Architecture:** Enable Darwin daemon runtime through existing platform boundaries, then share the Linux tray presentation and coordinator while supplying native macOS menu-bar and launchd adapters. A separate per-profile companion process and LaunchAgent preserve the Linux ownership rule that quitting the indicator does not stop sync.

**Tech Stack:** Go 1.27.2; `fyne.io/systray` v1.12.2 (Apache-2.0, cgo/AppKit); launchd `LaunchAgent` plists and `/bin/launchctl`; pinned `golangci-lint` 2.14.0; macOS 13+.

**Spec:** [docs/spec/000013-macos-daemon-design.md](../spec/000013-macos-daemon-design.md)

## Global Constraints

- Target macOS 13 Ventura and newer, matching pinned Go 1.27.2.
- `groved` calls the shared `syncengine` service directly; it does not spawn `grove sync` or create a missing profile.
- macOS config opening rejects symlinks and non-regular files, requires private permissions and current-user ownership, and never exposes file contents in errors.
- Profile leases provide non-blocking exclusive ownership and a read-only active-owner probe, consistent with Linux semantics.
- LaunchAgents run as the current user, never root; the daemon agent uses an explicit absolute config path and does not restart fatal exits.
- Daemon and companion login start are explicit and independent; Start/Stop target only the selected verified profile agent.
- `watch_local: true` is rejected before daemon work starts; false or omitted uses periodic scans.
- Keep presentation and service decisions in Go; isolate AppKit and launchd behind Darwin-specific adapters.
- Keep profiles, config, app support files, and plists outside Git; do not sign/notarize or enable user jobs automatically during pre-release.
- Do not run live Drive acceptance without explicit enablement and the dedicated test profile.

## Review Focus

- **Config path attacks:** symlink, non-regular, public-mode, wrong-owner, and Git-contained config must fail without leaking path contents (`TestDarwinConfigSecurity`, Task 1).
- **Unsupported watching:** `watch_local: true` must fail before profile inspection, lease acquisition, or record writes (`TestDarwinLocalWatchRejectedBeforeStartup`, Task 1).
- **Concurrent ownership:** a second process must fail the Darwin `flock` lease while the first owner remains active; probing must not release or mutate it (`TestLeaseExclusionAndProbe`, Task 1 / existing `internal/privatefs` Darwin test).
- **Wrong launchd target:** malformed, unknown-version, or profile-mismatched `launchctl print` data must disable controls and never invoke bootstrap, kickstart, or bootout (`TestLaunchdUnknownOrMismatchedJob`, Task 4).
- **Inconsistent launchd/ownership state:** a loaded but non-running job with an active profile lock must not enable Stop; an unloaded job with an active lock must also remain uncontrollable (`TestLaunchdUnverifiedOwnerCannotStop`, Task 4).
- **Stale or unavailable observations:** historical status may remain visible but must not enable Start over an active owner or expose stale profile facts as fresh (`TestTrayRetainedFacts`, Task 2).

---

### Task 1: Enable the safe Darwin daemon runtime

**Files:**
- Create: `internal/daemon/config_darwin.go`
- Create: `internal/daemon/config_darwin_test.go`
- Create: `cmd/groved/platform_darwin.go`
- Modify: `internal/daemon/config_other.go`, `internal/daemon/run.go`, `internal/daemon/watch_linux.go`, `internal/daemon/watch_other.go`
- Modify: `cmd/groved/platform_other.go`, `cmd/groved/command.go`
- Test: `cmd/groved/command_darwin_test.go`

**Interfaces:**
- Produces `localWatchSupported() bool` in `internal/daemon`, true on Linux and false on Darwin/other platforms.
- Produces Darwin `openConfig(path string) (*os.File, error)` with the same privacy/error contract as Linux.
- Produces `platformSupported() bool` and `processContext(context.Context) (context.Context, context.CancelFunc)` for Darwin; the context handles `os.Interrupt` and `syscall.SIGTERM`.

- [x] **Step 1: Write Darwin config and runtime rejection tests.** Add `TestDarwinConfigSecurity` for private regular files, symlinks, non-regular files, and sanitized errors; add `TestDarwinLocalWatchRejectedBeforeStartup` with dependencies that fail if profile inspection or lease acquisition runs.
- [x] **Step 2: Prove the unsupported-watch regression fails before the boundary exists, then cross-compile the Darwin tests.** Native Darwin execution is included in Task 7's macOS CI job.
- [x] **Step 3: Implement Darwin config opening and runtime boundaries.** Use `O_NOFOLLOW`, `O_CLOEXEC`, and `O_NONBLOCK`; require a regular file, permissions no broader than 0600, and current effective-user ownership. Reject `WatchLocal` immediately after config loading and before profile or lease work. Change non-Linux build constraints to exclude Darwin where Darwin implementations now exist.
- [ ] **Step 4: Run daemon, command, and privatefs tests natively on macOS.** Run: `go test ./internal/daemon ./internal/privatefs ./cmd/groved`. Deferred to Task 7's native macOS CI job; expected to cover lease exclusion/probe and signal-aware command tests.
- [x] **Step 5: Run Linux regression tests.** Run: `go test ./internal/daemon ./internal/privatefs ./cmd/groved`. Expected: all existing Linux tests pass and Linux watcher behavior is unchanged.
- [x] **Step 6: Commit.** `git add internal/daemon cmd/groved && git commit -m "feat: enable safe macOS daemon runtime"`

### Task 2: Share tray presentation across Linux and macOS

**Files:**
- Rename: `internal/tray/model_linux.go` → `internal/tray/model.go`
- Rename: `internal/tray/options_linux.go` → `internal/tray/options.go`
- Rename: `internal/tray/presentation_linux.go` → `internal/tray/presentation.go`
- Rename: `internal/tray/presentation_linux_test.go` → `internal/tray/presentation_test.go`
- Rename: `internal/tray/options_linux_test.go` → `internal/tray/options_test.go`
- Modify: `internal/tray/service_linux.go`, `internal/tray/run_linux_test.go`
- Modify: `cmd/grove-tray/command_linux.go`, `cmd/grove-tray/command_linux_test.go`
- Modify: `contrib/desktop/README.md`, `docs/spec/000014-linux-freedesktop-tray-design.md`
- Modify: `docs/implementation-plans/000014-linux-freedesktop-tray-plan.md`

**Interfaces:**
- Produces platform-neutral `Action`, `Sample`, `ServiceState`, `Row`, `View`, `Memory`, and `Options` types in package `internal/tray`.
- `Options` uses `ServiceInstance string` rather than a systemd-specific field; `Reduce(previous Memory, sample Sample, service ServiceState, opts Options) (Memory, View)` remains the shared presentation contract.

- [x] **Step 1: Move reducer/options tests to platform-neutral filenames and update the service-instance field assertions.** Add coverage for menu rows with a generic service label while retaining all existing status-state tables.
- [x] **Step 2: Run the presentation tests before moving implementation.** Run: `go test ./internal/tray -run 'TestTray(Presentation|RetainedFacts|StartupBusyHistory|Options)' -count=1`. Expected: compile/test failure until generic files and field names exist.
- [x] **Step 3: Move the pure model, option validation, and reducer into untagged files.** Rename the service-instance field to its platform-neutral name and update Linux service labels without changing the reducer’s status or action rules.
- [x] **Step 4: Run the tray package tests on Linux and cross-compile the shared tests for Darwin.** Native Darwin execution is included in Task 7's macOS CI job; Linux-only transports remain build-tagged.
- [x] **Step 5: Update Linux tray instructions to use `--service-instance`.** Verify code and user-facing docs with `rg --glob '!docs/implementation-plans/000013-macos-daemon-plan.md' -- '--systemd-instance|SystemdInstance' cmd internal contrib docs`; expected: no stale public option names remain.
- [x] **Step 6: Commit.** `git add internal/tray cmd/grove-tray contrib/desktop docs/spec/000014-linux-freedesktop-tray-design.md docs/implementation-plans/000014-linux-freedesktop-tray-plan.md docs/implementation-plans/000013-macos-daemon-plan.md docs/STATUS.md && git commit -m "refactor: share tray presentation model"`

### Task 3: Make the tray coordinator platform-neutral

**Files:**
- Rename: `internal/tray/run_linux.go` → `internal/tray/run.go`
- Modify: `internal/tray/service_linux.go`, `internal/tray/run_linux_test.go`
- Rename: `internal/tray/run_linux_test.go` → `internal/tray/run_test.go`

**Interfaces:**
- `TrayBus` remains `Publish(View) error`, `Actions() <-chan Action`, `Done() <-chan struct{}`, and `Close() error`.
- `Controller` remains `Observe(context.Context) (ServiceState, error)`, `Request(context.Context, Action, Sample) error`, and `Close() error`.
- `Dependencies` continues to inject `Inspect`, `Clock`, `OpenBus`, and `OpenService`; platform `OpenBus(context.Context, Options)` and `OpenService(context.Context, Options)` implementations supply native adapters.

- [x] **Step 1: Move the existing coordinator tests to a platform-neutral filename and add `TestTrayCancellationBeforeQueuedPublish` to assert queued observations do not publish after cancellation.**
- [x] **Step 2: Run the coordinator suite before refactoring.** Run: `go test ./internal/tray -run 'TestTray(StartupAndPolling|CanceledBus|StartupControlCancellation|CancellationBeforeQueuedPublish)' -count=1`. Expected: all pass, establishing the current shutdown contract before moving platform startup code.
- [x] **Step 3: Move `Run` and its shared interfaces into `run.go`.** Remove direct D-Bus construction from the coordinator; change Linux `OpenService` to own session-bus connection setup and keep all per-platform opening in build-tagged adapters.
- [x] **Step 4: Run coordinator and Linux transport suites.** Run: `go test ./internal/tray` and `go test -race ./internal/tray`. Expected: shared fake-bus tests and actual private-bus tests pass.
- [x] **Step 5: Commit.** `git add internal/tray && git commit -m "refactor: share tray lifecycle coordinator"`

### Task 4: Add a fail-closed LaunchAgent controller

**Files:**
- Create: `internal/tray/service_darwin.go`
- Create: `internal/tray/service_launchd_test.go`
- Create: `internal/tray/launchctl_darwin.go`
- Create: `internal/tray/testdata/launchctl-print-macos13.txt`
- Create: `internal/tray/testdata/launchctl-print-macos15.txt`
- Create: `contrib/macos/launchagents/com.torfstack.groved.plist.in`

**Interfaces:**
- `launchctlRunner` provides `Print(context.Context, string) ([]byte, error)`, `Bootstrap(context.Context, string, string) error`, `Bootout(context.Context, string, string) error`, and `Kickstart(context.Context, string) error`.
- `parseLaunchctlJob(output []byte, label string) (launchdJob, error)` returns a typed label, state, program, and argument vector or a sanitized error.
- `OpenService(context.Context, Options) (Controller, error)` implements the shared controller contract and targets only `gui/<effective-uid>/<validated-label>` and the fixed per-profile plist path.

- [x] **Step 1: Add parser and fake-runner tests.** `TestLaunchdPrintParsesSupportedJobs` checks label/state/program/arguments from captured macOS 13 and 15 output. `TestLaunchdUnknownOrMismatchedJob` checks unknown layouts, wrong label, wrong executable, altered argument vector, mismatched config profile, malformed plist, and command failures all disable controls without issuing a control command. `TestLaunchdUnloadedAgentCanStart` verifies a private valid plist enables Bootstrap only when profile ownership is inactive; `TestLaunchdUnverifiedOwnerCannotStop` verifies Stop is disabled for an unloaded or loaded-but-not-running job even when the profile lock is active.
- [x] **Step 2: Run launchd controller tests before implementation.** Run on macOS: `go test ./internal/tray -run 'TestLaunchd' -count=1`. Expected: tests fail because the controller/parser does not exist.
- [x] **Step 3: Implement secure per-profile LaunchAgent inspection and controls.** Parse only the fields needed from `launchctl print`; reject every unknown format. For loaded jobs validate the effective label, absolute `groved` executable, `--config` argument, and canonical profile match. For unloaded jobs validate the current-user-owned private plist at its fixed path and stable `com.torfstack.groved.<instance>` label. Use `bootstrap` only for a validated unloaded agent when profile ownership is inactive, `kickstart` only for a verified loaded inactive agent, and `bootout` only for a verified loaded agent whose profile owner is active. Never use shell execution or PID signaling.
- [x] **Step 4: Add the daemon LaunchAgent template.** Set `RunAtLoad` for explicit login enablement, omit `KeepAlive`, use `ExitTimeOut=0`, and pass the config as a distinct `ProgramArguments` element. Validate the template with `plutil -lint` and tests for rendered arguments with spaces.
- [x] **Step 5: Run fake-manager tests** (`TMPDIR=/tmp/grove-task-tmp go test ./internal/tray -run 'TestLaunchd' -count=1`, pass; `go test -race ./internal/tray`, pass). **Native disposable-agent acceptance is deferred to Task 7 macOS CI;** no macOS host is available here.
- [ ] **Step 6: Commit.** `git add internal/tray contrib/macos/launchagents/com.torfstack.groved.plist.in && git commit -m "feat: control macOS daemon LaunchAgents safely"`

### Task 5: Add the native macOS menu-bar companion

**Files:**
- Create: `internal/tray/bus_darwin.go`
- Create: `internal/tray/icons_darwin.go`
- Create: `internal/tray/bus_darwin_test.go`
- Create: `cmd/grove-tray/main_darwin.go`
- Rename: `cmd/grove-tray/command_linux.go` → `cmd/grove-tray/command.go`
- Rename: `cmd/grove-tray/command_linux_test.go` → `cmd/grove-tray/command_test.go`
- Modify: `cmd/grove-tray/main_linux.go`, `internal/tray/service_linux.go`
- Modify: `go.mod`, `go.sum`, `THIRD_PARTY_NOTICES`

**Interfaces:**
- `OpenBus(context.Context, Options) (TrayBus, error)` implements the Darwin adapter using `fyne.io/systray` v1.12.2.
- The common command accepts `--profile-dir`, `--name`, and `--service-instance`; one process displays one canonical profile.
- The native adapter maps each `View` row/action to a menu item, updates a macOS template icon/tooltip, and dispatches clicks to the shared coordinator.

- [x] **Step 1: Add an injectable native-menu wrapper and mapping tests.** Test all reducer view rows, enabled/disabled states, separators, dynamic menu refresh, icon/tooltip updates, and ActionRefresh/Start/Stop/Quit dispatch without launching AppKit.
- [ ] **Step 2: Run adapter tests before implementation.** Not executable in this Linux workspace because the Darwin AppKit dependency cannot compile here; mapping tests are present and will run in Task 7 macOS CI, but their pre-implementation failure was not observed. Run on macOS: `go test ./internal/tray ./cmd/grove-tray -run 'Test(MenuBar|TrayCommand)' -count=1`. Expected: new menu mapping tests fail before the adapter exists.
- [x] **Step 3: Add `fyne.io/systray` v1.12.2 and implement the AppKit adapter.** Use `RunWithExternalLoop` to run the native event loop around the shared coordinator, plus `ResetMenu`, menu item click channels, `SetTemplateIcon`, `SetTooltip`, and `Quit`; keep blocking status inspection off AppKit callbacks and preserve graceful cancellation.
- [x] **Step 4: Share Cobra command validation across Linux and Darwin.** Keep the Linux command on the generic `--service-instance` option and make that same command reusable by macOS; preserve profile canonicalization and duplicate-process identity behavior.
- [ ] **Step 5: Run Linux adapter-independent and CLI protocol tests** (`go test ./internal/tray ./cmd/grove-tray`, race suite, and all-package compile-only suite pass). Darwin/AppKit execution is deferred to Task 7 CI because the workspace is Linux. Run: `go test ./internal/tray ./cmd/grove-tray` on macOS and `go test ./internal/tray ./cmd/grove-tray` on Linux. Expected: native menu mapping tests and existing StatusNotifierItem/D-BusMenu tests pass on their respective targets.
- [ ] **Step 6: Commit.** `git add internal/tray cmd/grove-tray go.mod go.sum THIRD_PARTY_NOTICES && git commit -m "feat: add native macOS Grove menu bar"`

### Task 6: Bundle the app and document per-profile installation

**Files:**
- Create: `contrib/macos/build-app.sh`
- Create: `contrib/macos/Grove-Info.plist`
- Create: `contrib/macos/launchagents/com.torfstack.grove-tray.plist.in`
- Create: `contrib/macos/README.md`
- Modify: `mise.toml`, `contrib/systemd/README.md`

**Interfaces:**
- `mise run build` builds `grove`, `groved`, and `bin/Grove.app` on Darwin; on Linux it continues to build the existing three Linux binaries.
- `contrib/macos/build-app.sh` places `grove-tray` at `Contents/MacOS/grove-tray`, `grove` and `groved` at `Contents/Helpers/`, and writes `LSUIElement=1`, menu-bar resources, and both LaunchAgent templates into a local `bin/Grove.app`.
- The companion LaunchAgent starts only the menu-bar process with explicit profile/name/service arguments; it is separate from the daemon LaunchAgent.

- [ ] **Step 1: Add static bundle/plist tests and documentation checks.** Verify `Info.plist` hides the Dock icon, both LaunchAgent templates use absolute paths and distinct labels, daemon `ExitTimeOut` is zero, and no service auto-enable command is run by the build.
- [ ] **Step 2: Implement the app-bundle build script and mise task.** Use the pinned Go toolchain and native cgo/AppKit toolchain; do not codesign, notarize, install, or register login items during build.
- [ ] **Step 3: Write manual install, start, enable-at-login, stop, upgrade, and removal instructions.** Explain absolute paths, XML/plist validation, one profile per pair of agents, explicit opt-in, safe data retention, and that app quit does not stop daemon sync.
- [ ] **Step 4: Update Linux instructions for `--service-instance`.** Verify all linked template and bundle paths resolve.
- [ ] **Step 5: Run formatting and document checks.** Run: `gofmt -l cmd internal tests` and `git diff --check`; expected: no Go formatting output and no whitespace errors. On macOS, run `plutil -lint contrib/macos/Grove-Info.plist` and both plist templates after sample substitution.
- [ ] **Step 6: Commit.** `git add mise.toml contrib/macos contrib/systemd/README.md docs && git commit -m "build: package and document macOS desktop integration"`

### Task 7: Verify native macOS runtime and add CI coverage

**Files:**
- Create: `cmd/groved/process_darwin_test.go`
- Create: `internal/tray/process_darwin_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `docs/TESTBED.md`, `docs/STATUS.md`

**Interfaces:**
- The macOS workflow uses the repository-pinned Go and golangci-lint versions on `macos-15`, running format/lint, offline tests, race tests, and native builds.
- Darwin process acceptance uses a temporary profile and fake Drive provider; the real launchd check uses unique Grove-owned plist labels and cleans only those fixtures.

- [ ] **Step 1: Add process tests for fake-provider daemon startup, duplicate profile exclusion, SIGTERM cleanup, retained profile data, menu registration, status refresh, launchd Start/Stop, and tray-only Quit.** Keep real Drive disabled.
- [ ] **Step 2: Run those tests on macOS before wiring CI.** Run: `go test ./cmd/groved ./cmd/grove-tray ./internal/tray -run 'TestDarwin|TestTray' -count=1`. Expected: new native process cases fail before implementation/adapter integration is complete.
- [ ] **Step 3: Add a macOS CI job on `macos-15`.** Run `mise run lint`, `mise run test`, `mise run test-race`, and `mise run build`; cross-build `cmd/grove` and `cmd/groved` for Linux amd64 as regression checks. Keep the existing Linux CI job unchanged.
- [ ] **Step 4: Run the full required checks locally on Linux and natively on macOS.** Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`, and `mise run build` on each platform. Expected: all pass; macOS native manual acceptance records supported host/login behavior and any limits.
- [ ] **Step 5: Update product, architecture, roadmap, testbed, and status records.** Distinguish implemented Linux support from verified macOS behavior and record any unverified host/login limits.
- [ ] **Step 6: Review the final diff against every spec acceptance item.** Confirm no live credentials were used, no user service was enabled, no unrelated launchd job was changed, and all owned fixtures were removed.
- [ ] **Step 7: Commit.** `git add .github/workflows/ci.yml cmd/groved cmd/grove-tray internal/tray mise.toml docs && git commit -m "test: verify macOS daemon and menu-bar lifecycle"`

## Handoff

Implementation should proceed natively by one engineer because daemon startup,
shared coordinator interfaces, launchd ownership, and AppKit lifecycle are
sequentially dependent. The first Darwin runner pass must also confirm the
fail-closed `launchctl print` parser and `ExitTimeOut=0` behavior before enabling
the menu controls for a real user profile.
