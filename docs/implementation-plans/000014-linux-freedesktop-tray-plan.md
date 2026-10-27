# Linux Freedesktop Tray Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for the user's preserved native execution preference. Read the spec first and implement task by task with checkbox tracking. The user approved this plan for native implementation.

**Goal:** Give each selected Grove profile a persistent Linux tray icon with honest offline status and optional validated systemd user-service controls.

**Architecture:** A separate Go companion reads `daemon.Inspect` in-process and reduces observations into a deterministic presentation. A session-D-Bus adapter publishes StatusNotifierItem and D-BusMenu; an optional systemd adapter observes and controls only the configured matching user unit. One coordinator owns presentation updates and serializes refresh/control requests.

**Tech Stack:** Pinned Go 1.27.2 and existing Cobra/privatefs/daemon packages; add `github.com/godbus/dbus/v5 v5.2.4` (native Go, BSD-2-Clause). Private `dbus-daemon` instances provide protocol tests; Waybar and an independent host provide desktop acceptance. No GUI toolkit or daemon IPC.

**Spec:** [Approved spec 000014](../spec/000014-linux-freedesktop-tray-design.md).

## Global constraints

- Linux only; preserve Linux CLI/daemon behavior and macOS cross-builds of both existing binaries.
- Required absolute `--profile-dir`; optional `--name` defaults to `Grove`; optional `--systemd-instance` permits only letters, digits, underscores, and hyphens.
- One icon per canonical profile per session bus; no profile creation or path/remote-identity publication.
- Immediate status observation, then every two seconds; one inspection at a time, coalesced refreshes, prompt D-Bus handlers.
- Reuse `daemon.Inspect`; no Drive/OAuth access, lifetime lease acquisition, private-state decoding, or second sync engine.
- Ownership comes from the live lease probe; attempt/phase records are historical. Busy/unavailable data never clears last-known conflicts or proves fresh health.
- Normal and stopped SNI items use `Active`; warnings use `NeedsAttention`; never hide idle status with `Passive`.
- Export `org.kde.StatusNotifierItem`, register with `org.kde.StatusNotifierWatcher`, and provide `com.canonical.dbusmenu`.
- Menu actions: refresh; optional matching-unit start/stop; Quit tray. No direct service actions from icon activation.
- Never enable/install services, change lingering/mode/policy, force a stop, retry recovery, or perform automatic start as a tray side effect.
- Keep credentials, tokens, generated Drive IDs, filenames, profile/config/destination paths, session IDs, and raw errors out of public D-Bus data and logs.
- Current-run data safety remains mandatory; Grove is pre-release, so earlier-release migrations are unnecessary.
- Required Go verification: `mise run fmt`, `mise run lint`, `mise run test`, and `mise run test-race` for concurrent code. Preserve pinned tool versions. Consult the recorded host mise issue before invocation; if it persists, use exact pinned task-equivalent binaries and document the limitation rather than modifying unrelated host tools.
- No live Drive tests or persistent changes to the user's bar/services during acceptance. Use private buses, disposable profiles, test units, and isolated graphical sessions.

## Review focus

1. Profile busy on startup or missing later: remain explicitly unvalidated/unavailable, never recreate it, and never invent zero conflicts; Tasks 1 and 4.
2. Bar watcher replacement races with a delayed registration reply: only the current owner may establish registration; Task 2.
3. systemd emits job completion before StartUnit/StopUnit returns: correlate without losing completion or enabling duplicate requests; Task 3.
4. A custom unit/config changes after the menu opens: revalidate at dispatch, use fixed unit targeting, and keep failed validation visible; Tasks 3 and 4.
5. Quit or session-bus loss occurs during inspection/control: release only tray resources, preserve an already accepted service job, and join workers; Tasks 4 and 6.

## Files and responsibilities

- Create `internal/tray/options_linux.go`, `model_linux.go`, `presentation_linux.go` and matching `_linux_test.go` files: validated options/identity and pure status/menu reduction.
- Create `internal/tray/bus_linux.go`, `item_linux.go`, `menu_linux.go`, `icons_linux.go` and matching tests: SNI lifecycle, properties, D-BusMenu serialization and embedded icons. Store checked-in icon assets in `internal/tray/icons/`.
- Create `internal/tray/testbus_linux_test.go`: isolated bus fixture and fake watcher/client helpers shared by package tests.
- Create `internal/tray/service_linux.go`, `service_linux_test.go`: optional systemd observation, effective-command/config validation, and asynchronous jobs.
- Create `internal/tray/run_linux.go`, `run_linux_test.go`: coordinator, status worker, presentation publication, cancellation, and action dispatch.
- Create `cmd/grove-tray/main_linux.go`, `command_linux.go`, `command_linux_test.go`, `process_linux_test.go`: CLI wiring, process signals, and real-binary bus acceptance.
- Modify `go.mod`, `go.sum`, `mise.toml`, `.github/workflows/ci.yml`: pinned library, Linux tray build, explicit protocol-test prerequisites.
- Create `THIRD_PARTY_NOTICES.md`: dependency attribution required when distributing binaries.
- Create `contrib/desktop/README.md` and `contrib/desktop/grove-tray.desktop.in`; update `README.md`, product/architecture, roadmap, testbed, status, spec, plan, and decision 0013 with actual support/evidence.

## Task 1: Validated profile options and deterministic presentation

**Interfaces:** In package `tray`, define `Options{ProfileDir, Name, SystemdInstance string}` and `NormalizeOptions(Options) (Options,error)`. Define `Sample{Status daemon.StatusReport; Err error}`, `ServiceState{Unit, Phase, Reason string; CanStart, CanStop bool}`, `Action` constants `ActionNone/Refresh/Start/Stop/Quit`, and `Row{ID int32; Label string; Enabled, Separator bool; Action Action}`. `View{Status, IconName, TooltipTitle, TooltipText string; Rows []Row}` is transport-independent. `Memory` retains last-known profile facts and observed identity. `Reduce(previous Memory, sample Sample, service ServiceState, opts Options) (Memory,View)` is pure. `Identity(canonicalProfile string) string` returns a full SHA-256 hex digest for a stable private-path-derived identity.

- [x] Write `TestNormalizeOptions`: reject relative/missing/inside-Git/non-directory profile paths and non-regular/missing state files; resolve canonical aliases without reading credentials. Validate name as UTF-8, 1–80 Unicode code points, single-line with no control characters; allow ordinary Unicode/markup characters as text. Reject empty/invalid supplied service labels; omitted labels disable controls. Errors never echo paths/values.
- [x] Write `TestTrayPresentation`: table-driven exact labels/status/icon states for every observation row in the spec, including failed/busy/cancelled attempts, remote-check warnings, no record, active ownership with stale stopped phase, and incomplete population. Assert waiting says `Last sync completed`, never `Up to date`.
- [x] Write `TestTrayRetainedFacts`: fresh conflicts/recovery → busy/failed sample retains explicitly last-known facts; only a fresh validated profile sample clears them. Unknown ownership/error overrides normal health. Startup busy is not validated health. A changed profile identity clears retained facts. Assert fixed row IDs and separate service/ownership labels.
- [x] Run `go test ./internal/tray -run 'TestNormalizeOptions|TestTrayPresentation|TestTrayRetainedFacts'`; observe missing production symbols, then implement these files with canonical/private path helpers and the spec's precedence. Keep filesystem validation in options/observation, outside the reducer.
- [x] Run the focused tests; expect PASS. Update STATUS and commit this independently testable presentation boundary.

## Task 2: Session-bus tray item and host-rendered menu

**Interfaces:** `OpenBus(ctx context.Context, opts Options) (*Bus,error)` connects privately to the session bus, owns `org.grove.Tray.p<Identity>`, and exports the item/menu. Its internal constructor `openBus(ctx context.Context, opts Options, conn *dbus.Conn) (*Bus,error)` accepts an owned private connection for tests. `(*Bus).Publish(View) error` atomically replaces its published snapshot; `Actions() <-chan Action` is bounded/coalesced; `Done() <-chan struct{}` reports connection loss; `Close() error` is idempotent and joins registration work. `NewTestBus(t *testing.T) string` starts `dbus-daemon --session --nofork` with a private temporary socket/config and returns its address; cleanup terminates/joins only that process. Tests dial that address explicitly and never use the user's bus or shared environment mutation.

- [x] Pin godbus v5.2.4 when first importing it; retain its BSD-2-Clause notice for binary redistribution. Use private connections and context-aware method calls. Add the isolated bus helper; a missing `dbus-daemon` is a clear prerequisite failure on Linux, never a passing skipped protocol suite.
- [x] Write `TestSNIRegistration`: fake watcher sees `/StatusNotifierItem`; a second identical canonical-profile identity fails exclusive nonqueued ownership, another profile succeeds, and no profile path/label appears in bus names. No watcher is a supported waiting state. Watcher replacement and delayed old replies cannot suppress registration with the new owner; missing bus fails promptly; disconnection closes Done.
- [x] Write `TestSNIPropertiesAndSignals`: introspection and `Get/GetAll` expose read-only `Category=ApplicationStatus`, stable `Id`, public `Title`, `Status`, `IconName/IconPixmap`, attention-icon equivalents, `ToolTip`, `Menu=/MenuBar`, `ItemIsMenu=true`, and `WindowId=0`. Unknown properties are protocol errors; unchanged views emit no change signals; relevant updates emit the required SNI/property notifications.
- [x] Write `TestDBusMenuWire`: verify Version 3; recursive layout signature `(ia{sv}av)`; correct depth/property filtering; stable IDs; labels, separators and enabled states; GetLayout/GetGroupProperties/GetProperty, Event/EventGroup, AboutToShow/AboutToShowGroup and update signals. Unknown IDs and disabled rows cannot dispatch actions. Duplicate refreshes coalesce; icon Activate/SecondaryActivate/ContextMenu never dispatch Start/Stop. Escape tooltip markup and double menu-label mnemonic underscores without leaking raw errors.
- [x] Observe failures with `go test ./internal/tray -run 'TestSNI|TestDBusMenu'`. Implement host registration against its current unique owner, subscribe before initial discovery, and retry transient registration with bounded backoff (200ms to 5s) while that owner remains current. Never own a watcher/host name. A hostless period produces one sanitized diagnostic per transition.
- [x] Add four Grove-owned named SVG icons (idle, syncing, stopped, attention) and matching embedded PNG fallbacks at 16/22/32/64 pixels. Convert decoded PNG pixels to SNI network-order ARGB. `TestIconPixmaps` verifies dimensions/data lengths/alpha conversion and all semantic states have a fallback. No runtime icon font, toolkit, or external renderer.
- [x] Run package tests and race tests; expect PASS without a real desktop bus. Update STATUS and commit the dependency, transport, menu, icons, and tests.

## Task 3: Validated optional systemd controls

**Interfaces:** `OpenService(ctx context.Context, conn *dbus.Conn, opts Options) (*Service,error)` is used only when an instance is supplied. `(*Service).Observe(ctx context.Context) (ServiceState,error)`, `Request(ctx context.Context, action Action, current Sample) error`, and `Close() error` own only the selected unit's subscriptions/jobs. Observe reports manager/mapping eligibility; Reduce additionally gates start on confirmed inactive profile ownership. Request receives a freshly acquired status sample and rechecks ownership as well as the unit/config mapping. Use a fixed `groved@<instance>.service` target, systemd's user-session bus destination, and `StartUnit/StopUnit` with job mode `fail` to avoid replacing unrelated queued jobs. All returned/public errors use fixed categories.

- [ ] Write `TestServiceMapping`: load unit without starting it; read effective `ExecStart`; accept exactly one direct executable whose basename is `groved` with argv containing only its program and `--config ABSOLUTE_PATH` (also accept Cobra's `--config=ABSOLUTE_PATH`). Reject wrappers/multiple commands, extra arguments, absent or unsafe/private-config failures, and canonical profile mismatch. Use `daemon.LoadConfig`, not duplicate config decoding. Test custom config paths, aliases, spaces, and unit aliases whose resolved ID differs from the fixed target.
- [ ] Write `TestServiceAvailability`: absent manager is read-only tray capability loss; inactive/failed validated unit permits start only with confirmed inactive profile ownership; active unit permits stop; unknown/transitional/queued-job state disables controls. Unit state never substitutes for daemon ownership. Revalidate executable/config/profile immediately before every request.
- [ ] Write `TestServiceJobs`: fake manager proves fixed unit/mode, prompt handlers, one pending request, accepted start not reported as completed sync, long stop stays pending, job failure and access denial visible, early JobRemoved before method reply retained, and manager-owner loss invalidates observations. Reconnection requires a fresh validated sample; accepted jobs are never canceled by Close.
- [ ] Observe failing focused tests, then implement config validation plus manager subscription before dispatch. Bound each RPC to five seconds; a timed-out mutation is `outcome unknown`, not permission to retry. Reconcile current unit/job state before reenabling. A five-second RPC budget never imposes a daemon stop deadline.
- [ ] Run `go test ./internal/tray -run TestService` and `go test -race ./internal/tray`; expect PASS with fake managers on private buses and no host-unit changes. Update STATUS and commit.

## Task 4: Observation/coordinator lifecycle

**Interfaces:** `Dependencies` injects `Inspect func(context.Context,string)(daemon.StatusReport,error)`, `Clock daemon.TimerClock`, and bus/service factories for tests. `Run(ctx context.Context, opts Options, deps Dependencies) error` normalizes options, performs startup observation, opens bus/optional service, and owns all workers through cleanup. Production Inspect wraps `daemon.Inspect(ctx, profile, syncengine.Service{})`; provide a local real TimerClock implementation without exporting daemon internals.

- [ ] Write `TestTrayStartupAndPolling`: immediate observation; next observation two seconds after completion; max one active inspection; repeated menu/clock refreshes collapse to one pending refresh. Snapshot/menu requests stay prompt while inspection is blocked. A busy existing profile starts with an unavailable/busy view; malformed/missing startup profile fails before advertising normal status; later removal remains visible/unavailable without recreating it.
- [ ] Write `TestTrayProfileUnavailable`: removal or invalidation after startup produces unavailable state and disables start; restored valid observations may recover without creating state or retaining false healthy counts. Retained facts stay within the selected canonical profile and this companion lifetime. No profile-binding value enters public views.
- [ ] Write `TestTrayActions`: Refresh only rereads; Start/Stop require a current validated mapping and fresh ownership; unknown/disabled/stale IDs do nothing; no automatic starts on launch/host appearance/recovery from read error; Quit never stops a service. Allow one control worker; publishing and status observations remain responsive while its RPC/job is pending.
- [ ] Write `TestTrayShutdown`: signal, Quit, bus loss, and genuine Publish/Close failures preserve sanitized error classification; cancel/join status/registration/control workers; accepted manager jobs continue; no daemon lease taken or released. Race tests exercise simultaneous refresh, quit, and job completion.
- [ ] Observe failing focused tests. Implement coordinator-owned state, one inspection worker, bounded action/completion mailboxes, and immutable published snapshots. Cancellation joins only owned work and never clears journals/state. Make partial observation errors immediately visible with historical retention rules from Task 1.
- [ ] Run package normal/race tests; expect PASS. Update STATUS and commit.

## Task 5: User command, distribution assets, and CI integration

**Interfaces:** `cmd/grove-tray/newCommand(run func(context.Context,tray.Options) error) *cobra.Command` exposes `--profile-dir`, `--name`, and `--systemd-instance`; its Linux main uses SIGINT/SIGTERM and the existing sanitized stderr/exit pattern. All command/package sources have Linux build constraints.

- [ ] Write `TestTrayCommand`: required absolute profile, no positional args, exact flag routing/default label, invalid instance/name refusal, successful help without bus/filesystem access, canceled context, and sanitized service failure. Observe failures, then implement command/main wiring.
- [ ] Add the tray build to `mise run build` on Linux; preserve existing builds on macOS. Update the task description and Linux CI to verify `dbus-daemon` availability explicitly before normal/race tests. CodeQL's existing `go build ./...` includes the new binary without a separate workflow. Keep existing immutable action pins and Go/lint versions.
- [ ] Add desktop-entry template with a documented absolute executable and explicit profile placeholder, `Terminal=false`, public Name/Icon, and manual installation into `$XDG_CONFIG_HOME/autostart` only at the user's choice. The shipped `.desktop.in` is inert until customized. Document Desktop Entry Exec quoting separately from shell quoting, including spaces, percent signs and backslashes; do not imply shell expansion works there.
- [ ] Document installing icon assets into the user's XDG data icon directory, manual foreground launch, optional service linkage, multiple profiles, no-host diagnostics, tray quit vs daemon stop, and the Waybar `tray` module in `modules-right`. Preserve existing bar configuration; provide an example rather than an automatic editor.
- [ ] Run command/package normal/race tests and Linux build; cross-build `./cmd/grove` and `./cmd/groved` for Darwin arm64. Verify the new command is omitted from Darwin package enumeration. Update STATUS and commit.

## Task 6: Real-process/desktop acceptance and final integration review

**Interfaces:** `cmd/grove-tray/process_linux_test.go` builds/runs the actual companion against a private bus and disposable existing profile, using the same shared-engine fixture pattern as `cmd/groved/process_test.go`. Desktop/operator acceptance is documented in `docs/TESTBED.md`; it does not silently run during ordinary tests.

- [ ] Add `TestTrayProcessLifecycle`: observe registration/properties/menu through an independent client, force status transitions with disposable records and real profile/daemon leases, kill/recreate the fake watcher, reject a duplicate companion, activate Refresh/Quit, send SIGTERM, and verify expected exit/resource cleanup. Use no live OAuth/Drive endpoints. Assert profile/journal bytes and daemon lease ownership stay unchanged by tray-only actions.
- [ ] Add `TestTrayProcessDaemon`: use a test helper daemon with fake Drive HTTP/provider wiring, actual daemon orchestration and real profile locks to demonstrate waiting/syncing/failure/stop transitions. Keep the test seam in test binaries; production CLI receives no endpoint bypass flags. Assert quitting the actual tray leaves the helper daemon running and its profile lease held.
- [ ] Run a temporary Waybar instance in an isolated Sway/headless graphical session with a private bus and only the tray module. Demonstrate icon/pixmap visibility, tooltip, host menu, status changes, bar restart, duplicate protection and tray-only quit. No restart/reconfiguration of the user's ordinary panel.
- [ ] Test a second independent SNI host (prefer installed KDE Plasma; otherwise an available independent panel/host) in isolation. Record its actual version and tested behaviors. If unavailable, explicitly retain broad interoperability as unverified and record the required next acceptance step; a fake host is protocol evidence, not desktop evidence.
- [ ] If the user manager is available, verify controls using a unique disposable `groved@<instance>.service`, explicit private config and temporary override. Match effective command/profile; start, observe lease, stop gracefully, and remove only owned artifacts. Do not enable anything or touch lingering. Otherwise record fake-manager coverage and the unverified runtime gate.
- [ ] Update PRODUCT/ARCHITECTURE/README/ROADMAP/TESTBED/STATUS/spec/decision and mark only demonstrated plan steps complete. Check relative documentation links, `git diff --check`, dependency notices and `go mod verify`.
- [ ] Run required pinned fmt/lint/full offline/race/build checks once on the integrated tree, plus both Darwin cross-builds. Repeat affected checks only for subsequent changes/failures. Expect PASS; distinguish skipped/unavailable desktop gates from successful acceptance.
- [ ] Invoke the requesting-code-review skill for one fresh whole-change review under the preserved native workflow. Assess findings, fix valid issues with meaningful regression coverage and fresh affected checks. Publication/review/merge follow the session's existing explicit authorizations and AGENTS requirements; never infer merge authorization from design/plan approval. If published, obtain one completed initial CodeRabbit review and retrieve its summary/comments/threads; fixes require fresh CI, not another CodeRabbit review.

## Self-review and handoff

All spec sections map to the six tasks: options/status/model (1/4), protocol and
icons/menu (2), controls (3/4), session lifecycle (2/4/5), installation (5), and
real-process/desktop acceptance and current product records (6). Review Focus
cases have explicit tests in their owning tasks. No engine mutation or daemon
IPC is required.

Implementers may refine internal helper types while preserving the explicit
interfaces and semantics above. The user approved this plan before implementation.
Native execution remains the user's existing
preference; no new execution-method choice is needed.

Dependency references: [godbus v5.2.4 release](https://github.com/godbus/dbus/releases/tag/v5.2.4),
[godbus API](https://pkg.go.dev/github.com/godbus/dbus/v5@v5.2.4), and
[systemd manager D-Bus API](https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html).
