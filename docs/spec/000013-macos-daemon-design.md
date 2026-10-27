# macOS desktop integration

Date: 2026-10-27. Status: approved scope; implementation plan pending review.

## Intent

Bring Grove's Linux desktop experience to macOS while keeping the shared Go
sync engine and profile behavior. The user requested the full desktop workflow:
a per-profile background service managed by launchd and a native menu-bar
companion for status and controls.

## Approaches

1. **Native macOS desktop integration (recommended and selected).** Enable the
   existing foreground daemon on macOS, provide an optional per-profile
   LaunchAgent, and add a native menu-bar companion with offline status and
   service controls. This matches the Linux systemd plus tray experience while
   keeping platform-specific lifecycle code behind boundaries.
2. **Foreground daemon only.** Run `groved` manually in a terminal. This is a
   smaller portability step, but lacks the desktop visibility and lifecycle
   controls the user asked to match.
3. **Add FSEvents and other native sync triggers too.** This could match Linux's
   optional local watching, but expands the work into filesystem event coverage
   and repair semantics. Keep it as a separate follow-up.

## Goals

- Run `groved --config /absolute/path` on macOS as a foreground process under
  the user's launchd session, using the existing periodic scheduler and shared
  sync engine.
- Provide an optional per-profile LaunchAgent template. Starting the agent
  explicitly starts sync; enabling its login launch remains a separate,
  explicit user choice.
- Provide a native menu-bar companion that shows one profile's status, tooltip,
  and menu; supports Refresh, Start, Stop, and Quit; and can be started at login
  independently of the daemon.
- Match Linux's honest status and control behavior: inspect offline, retain
  historical status separately from live ownership, validate the selected
  agent's effective executable and config before controlling it, and keep
  Start/Stop unavailable when the target cannot be verified.
- Keep quitting the menu-bar companion independent from daemon shutdown. Losing
  the graphical session must not cancel an accepted sync or release daemon
  ownership.
- Preserve one active writer per profile. Multiple profiles can use separate
  menu-bar processes and LaunchAgents, while existing profile and destination
  locks remain authoritative.
- Reject `watch_local: true` on macOS with a clear actionable error; omitted or
  false continues to use periodic scans. Use remote change polling when its
  existing platform-independent support permits it.
- Keep Linux service and StatusNotifierItem behavior unchanged. Add native macOS
  build and runtime verification, and cross-build both existing commands for
  Linux.

## Non-goals

- FSEvents, other local filesystem watching, or changing sync scheduling.
- Windows daemon or desktop support.
- Automatic profile discovery, hot reload, or management of arbitrary launchd
  jobs.
- Automatically installing or enabling a LaunchAgent or login item without an
  explicit user action.
- App Store distribution, automatic updates, or code signing/notarization while
  Grove remains pre-release.
- Authenticated macOS acceptance unless the dedicated test profile and native
  macOS environment are actually available and explicitly enabled.

## Constraints and design

- Grove remains pre-release. Current-run data safety and interruption recovery
  remain required; earlier-release migration is not.
- Target macOS 13 Ventura and newer, matching the minimum supported by pinned Go
  1.27.2.
- `groved` continues to call the shared `syncengine` service directly. It never
  spawns `grove sync` or creates a missing profile.
- Keep process signals, secure config opening, LaunchAgent control, and native
  menu-bar transport behind platform-specific boundaries. Reuse the Linux
  status reducer and service-control invariants where those can be made
  platform-neutral without weakening Linux checks.
- macOS config opening rejects symlinks and non-regular files, requires private
  permissions and current-user ownership, and never exposes file contents in
  errors. Use APIs available in the pinned Go version.
- Profile leases provide non-blocking exclusive ownership and a read-only
  active-owner probe, consistent with Linux semantics. Verify behavior on
  supported local macOS filesystems.
- The LaunchAgent runs as the current user and invokes `groved` with an explicit
  absolute config path. It does not run as root. Its template uses a stable
  label derived from a validated instance name, does not restart fatal exits,
  and waits for graceful shutdown without a forced timeout.
- Service controls target only the explicitly selected Grove LaunchAgent.
  Before Start or Stop, inspect loaded program arguments and private config and
  confirm the canonical profile matches the menu-bar instance. For an unloaded
  job, validate the private plist at the fixed per-profile path before loading
  it. Do not accept arbitrary labels or signal PIDs directly. A Stop action is
  available only when the selected job is verified as running and the profile
  has active ownership.
- Agent installation and login enablement are explicit operator steps, with
  manual instructions parallel to the systemd user-service guide. Installing
  or starting the menu-bar companion does not start the daemon. Enabling the
  daemon's LaunchAgent at login is separate from starting it now.
- The native menu-bar companion reads the same sanitized offline status model
  as `grove status`; it does not read tokens, call Drive, or mutate profile
  state. Refresh only rereads status. Quit closes only the indicator.
- Use a Go-first implementation for the menu-bar companion, with native macOS
  APIs isolated behind a small adapter. Keep presentation and daemon/service
  decisions in Go. The implementation plan selects `fyne.io/systray` v1.12.2
  for its AppKit status item and Go menu callbacks; its Apache-2.0 notice and
  cgo/AppKit build requirement must be recorded. Bundle the companion as an
  `LSUIElement` app. Use a per-profile LaunchAgent for the companion login start
  and a separate LaunchAgent for the daemon, so either can run independently.
- Use `launchctl print` only through a fail-closed parser for the effective
  service label, executable, and arguments. Its output is diagnostic text, not
  a stable data API; unsupported output disables service controls while status
  remains available. The implementation plan must test captured output and a
  disposable native agent on supported macOS.
- Set launchd `ExitTimeOut` to zero so normal Stop waits for daemon cleanup,
  consistent with the existing systemd service's unbounded graceful stop.
- Keep config, app support files, launchd plists, and profile data outside the
  repository. Installation documentation must describe safe upgrade and
  removal without deleting profiles or synced data.

## Acceptance

- Deterministic tests cover Darwin config validation, unsupported local
  watching, shared status reduction, and launchd target/config validation.
- Native macOS tests cover profile lease exclusion/probing and daemon lifecycle
  with fake sync dependencies, including graceful interruption and recovery-safe
  shutdown.
- A disposable LaunchAgent acceptance verifies install/load, active status,
  Start/Stop, login enablement behavior, fatal exit remaining stopped, and
  cleanup without touching ordinary user services.
- Native menu-bar acceptance verifies icon/status changes, tooltip/menu, refresh,
  validated service controls, companion login start, and Quit while the daemon
  remains active.
- Both `grove` and `groved` build and run natively on macOS; their Linux builds
  and existing Linux CI checks remain passing.
- Required project formatting, lint, offline tests, and race tests for concurrent
  code pass. Live Drive access remains separately gated.
- Product, architecture, installation docs, roadmap, and status describe the
  verified behavior and any unverified host or login claims.

## Open implementation choices

- Confirm on macOS 13+ that the selected systray event loop, launchd print
  parser, generated per-profile plists, and app bundle behave as specified.
- Confirm that `ExitTimeOut=0` and the chosen Stop operation preserve graceful
  shutdown and do not unload unrelated launchd jobs.
