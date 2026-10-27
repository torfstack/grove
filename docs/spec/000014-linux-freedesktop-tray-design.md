# Linux Freedesktop tray companion

Date: 2026-10-27. Status: approved and implemented on `feat/linux-tray`; final review pending.

Independent builder check: no important unanswered questions remain for planning.

## Intent and agreed direction

Give Grove a persistent icon in the user's Linux panel, with understandable sync
status and access to ordinary daemon controls. The user uses Waybar and selected
a general Linux Freedesktop tray integration rather than a Waybar-specific
status script. This draft develops the proposed separate `grove-tray` companion.

The user approved this design, including the author-proposed defaults, menu,
service control rules, and first-slice boundaries below.
Spec 000013 remains an unapproved macOS proposal; this draft takes priority for
the current design discussion without renumbering or replacing that document.

## Approaches

- **Selected direction: separate tray companion.** `grove-tray` publishes a
  StatusNotifierItem and a D-BusMenu on the graphical session bus. It reads the
  existing offline status service directly. It remains available when `groved`
  stops and works with compatible tray hosts, including Waybar. This adds a
  small executable and a D-Bus dependency.
- **Tray inside `groved`.** Fewer executables, but an icon would disappear when
  the daemon exits, and a headless/lingering daemon would acquire a graphical
  session responsibility. It cannot offer a useful stopped-daemon view.
- **Bar-specific status command.** Smallest integration for one panel, but
  duplicates presentation for other panels and does not provide the general
  tray interaction requested here.

## Scope

- One existing profile and one tray icon per invocation, with a public display
  label. Support multiple independent profile invocations.
- Show observed daemon ownership, last recorded attempt, conflicts/recovery
  when inspectable, and unavailable/busy observations honestly.
- Provide status details in the tooltip and menu, a refresh action, optional
  systemd user-service start/stop actions, and a separate quit-tray action.
- Preserve headless `groved`, its lifetime exclusion, and the shared sync engine.
- Use Go, Linux platform boundaries, and a pinned maintained D-Bus library.
  No GTK/Qt dependency is needed to render a host-owned icon and menu.

The first slice does not create profiles, authenticate, edit configuration,
resolve conflicts, retry recovery, trigger immediate sync, change sync modes,
enable/disable services, alter lingering, or provide pause/resume. It does not
add a full application window, desktop notifications, legacy XEmbed fallback,
or custom adapters for panels without a StatusNotifierItem host. A completed
attempt does not prove the remote tree is currently synchronized.

## Launch and identity

Proposed command:

```sh
grove-tray --profile-dir /absolute/path/to/profile --name Personal
```

`--profile-dir` is required, absolute, and canonicalized through Grove's existing
private filesystem/profile boundary. `--name` is optional, defaults to `Grove`,
and is a bounded plain-text public label, never a path-derived fallback. Invalid
startup arguments fail with sanitized errors. The profile must validate before
the normal icon is exposed; a temporarily locked existing profile may enter an
explicit unavailable/busy view and retry. An invalid or missing profile is never
created. A profile that becomes unavailable later keeps a visible error icon.

Use a stable item identity for the canonical profile, without publishing its
absolute path or remote identifiers in item names. Enforce one companion per
canonical profile on a session bus using exclusive bus-name ownership. A second
invocation reports that the indicator is already running without replacing it.
Different profiles get different identities even if their display labels match.

## Status observation and presentation

Call `daemon.Inspect` through a narrow injected interface in the same process;
do not repeatedly spawn `grove status` or decode private profile state separately.
This reuses the existing ownership probe, nonblocking profile locking, sanitized
summary, and historical record contract. It opens no OAuth session or Drive
connection and does not acquire the daemon's lifetime lease.

Proposed refresh is immediate, then every two seconds, with one inspection at a
time. Refresh requests coalesce. Menu opening may request an asynchronous refresh
but must return promptly from the latest snapshot; no D-Bus request waits for a
filesystem inspection. Emit icon/menu/tooltip changes only when their content
changes. Display last-attempt timestamps rather than a ticking elapsed counter.

| Observation | Presentation |
| --- | --- |
| Unknown ownership, inspection error, or malformed status | Status unavailable; attention icon |
| Confirmed conflicts or pending recovery | Needs attention; retain the observed daemon state in details |
| Inactive ownership | Daemon stopped; last attempt explicitly historical |
| Active ownership and recorded syncing phase | Syncing (last recorded); profile summary may be busy |
| Active ownership and last attempt failed | Last sync failed; daemon still running |
| Active ownership and waiting after completion | Waiting; last sync completed |
| Active ownership with no recorded attempt/startup | Running; no completed attempt recorded |
| Stopping or another transitional observation | Last recorded phase plus observed ownership |

These rows describe precedence. Unknown/error status overrides normal health;
confirmed unresolved conflicts/recovery remain visible even if the daemon is
stopped or syncing. Remote polling failure is a separate warning in details and
uses attention while its last recorded outcome remains failed; a later successful
check clears that warning. Busy attempts are labeled busy, never failed or
completed. Cancellation is historical, not evidence of a running attempt.

During profile-lock contention, show `Profile details busy`. Any retained
conflict/recovery facts are explicitly last known and cannot be cleared by an
unavailable sample. On read failure, mark retained facts historical immediately;
do not continue displaying an apparently fresh healthy result. Old record age
alone does not prove a daemon stopped: active ownership comes from the lease.
Bound retained data to the current session and selected profile identity.

Use distinguishable icon shapes as well as color for running/waiting, syncing,
stopped, and attention. Supply Grove-owned small-size icon assets with an icon
name and compatible pixmap fallback; do not rely on an icon font or a particular
theme. Exact artwork is left to implementation. The host controls placement and
may apply its own visibility preferences. Normal/stopped items use SNI `Active`
to remain discoverable; warnings use `NeedsAttention`, not `Passive`.

## Tooltip and menu

Tooltip: public label, observed ownership, last recorded phase/attempt outcome,
last attempt finish time, and conflict/recovery availability. Include configured
sync mode and warnings when available. Escape all text for the host's markup
rules. Exclude credentials, token/config/profile/destination paths, filenames,
remote identities, session identifiers, and raw errors from public D-Bus output.

The proposed flat menu contains:

1. Disabled informational rows for the label, ownership, last attempt, mode,
   conflict count/recovery, and observation availability.
2. `Refresh status`, which only rereads offline status.
3. Optional `Start <instance> service` / `Stop <instance> service`, enabled
   according to that explicitly configured service's observed manager state.
4. A separator and `Quit tray`, which only exits the companion.

There is no generic terminal-launch action or full status window in this slice.
Export the menu via D-BusMenu so compatible hosts render it themselves. Provide
the SNI menu property and `ItemIsMenu` semantics; mouse activation never
starts/stops a service directly. Stable menu IDs map only to known actions;
reject unknown IDs and recheck action availability at dispatch. Duplicate clicks
cannot launch concurrent service-control requests.

## Optional service controls

Add `--systemd-instance personal` to opt into controls for the fixed user unit
`groved@personal.service`. Accept the simple instance-label character set already
documented for Grove (letters, digits, underscores, hyphens), not arbitrary unit
names, shell commands, or paths. The ordinary tray protocol and offline status
remain usable on systems without a systemd user manager.

Use the user's systemd manager over D-Bus for state observation and `StartUnit`
and `StopUnit`. Always name the selected unit in the menu. Do not infer that the
service owns this profile from a matching PID or from daemon-status records.
Before enabling controls, and again before each request, verify the effective
unit has a supported direct `groved --config ABSOLUTE_PATH` invocation and load
that private config through Grove's existing loader. Its canonical profile must
match the tray's selected profile. Custom config paths are allowed; wrappers or
unverifiable overrides keep controls disabled with a fixed explanation. Unit or
config validation never opens credentials or starts sync.

Service state and profile ownership remain separate observations. A foreground
daemon can own the profile while the selected unit is inactive; show that and
disable start rather than promise a takeover. A service can be active while its
profile is unavailable; show both facts. Revalidate before dispatch, then allow
normal daemon locks to arbitrate races. No process is killed by PID.

Track the manager's asynchronous job/state after the user requests an action.
Report `Starting service` or `Stopping service` and disable repeated controls
until the outcome is observed. A start request accepted by systemd is not proof
of active profile ownership or a successful sync. A long graceful stop stays
visible as stopping; the tray never adds a kill timeout, cancels cleanup, or
automatically retries. Lost manager access or job tracking becomes an unknown
control outcome and requires fresh state before another action is enabled.

Quitting the tray or losing the graphical session does not stop the daemon.
Starting the tray, discovering a panel, or refreshing status never starts a
service. Installing or enabling user services remains an explicit operator step.

## Desktop protocol and lifecycle

Implement the widely deployed `org.kde.StatusNotifierItem` and
`org.kde.StatusNotifierWatcher` compatibility names used by Waybar, with
`com.canonical.dbusmenu` for the menu and normal D-Bus properties/introspection.
The Freedesktop specification also describes `org.freedesktop` names; do not
assume those names alone interoperate with actual panels. Wire compatibility
must be verified against Waybar and another independent host before claiming
broader tested support.

Register with the existing watcher; do not implement or replace a watcher/host.
Watch for watcher ownership changes and register again after a bar restart.
With a session bus but no tray host, remain ready for a later host and report
one sanitized diagnostic rather than repeatedly logging. Missing session-bus
access is a clear startup failure. On bus disconnection, exit with a sanitized
failure; session/autostart supervision may relaunch the companion. SIGINT,
SIGTERM, and Quit tray remove its bus ownership and release its own resources.

Provide manual installation and session-autostart instructions, a desktop-entry
template with explicit profile arguments, icon installation instructions, and a
minimal Waybar example enabling its `tray` module on the right. Do not install
anything into the user's live session or change their Waybar configuration as
part of implementation. Tray placement belongs to the panel; desktop support
depends on an installed/enabled host, including extensions where required.

## Architecture and dependency boundaries

Add `cmd/grove-tray` with a small presentation/coordinator package. Separate
observation, presentation reduction, tray transport, and optional service
control behind injectable boundaries. Keep the deterministic state-to-menu/icon
mapping independent of D-Bus. Use no new daemon IPC or engine mutation API.

The implementation selects and pins a maintained Go D-Bus library after checking
its licensing, wire support, cancellation, and testability. Prefer the smallest
dependency that handles the existing protocols; avoid a GUI toolkit solely for
a host-rendered menu. Restrict desktop code to Linux so both existing commands
continue to cross-build for macOS. Add the tray binary to Linux build/CI tasks.

## Verification and completion

- Deterministic presentation tests cover healthy, stopped, busy, failed,
  unavailable, conflicting, recovery-pending, and partially observable status;
  verify that stale facts cannot produce a fresh healthy presentation.
- An isolated session bus/fake host verifies registration, menu wire signatures,
  properties/signals, duplicate identity exclusion, watcher replacement, and
  graceful exit. Test menu events rather than implementation details.
- A fake systemd manager verifies explicit target/config matching, fixed unit
  targeting, denied/unavailable controls, duplicate clicks, asynchronous job
  failures, long shutdown, and loss of manager access.
- Real Waybar acceptance demonstrates icon visibility, state changes, tooltip,
  menu, bar restart, and Quit tray while daemon ownership remains unaffected.
  Use a disposable local profile and fake-provider daemon harness; no live
  credentials are needed. An independent host is a separate acceptance gate.
- Optional real user-manager controls use isolated units and disposable state;
  never start, stop, enable, or rewrite the user's ordinary daemon units.
- Required formatting, lint, offline tests, race checks for concurrent code,
  Linux builds, and existing macOS cross-builds pass. Live Drive tests require
  separate explicit authorization and are not necessary to prove tray behavior.
- Update product/architecture, installation docs, roadmap, testbed, and status
  with actual supported behavior and any unverified host claims.

## Proposed defaults and matters left to the builder

The author proposes one profile per icon, a two-second polling interval, the
flat menu above, and optional validated systemd controls. No additional product
choice is required to review this draft. The builder selects package names,
exact D-Bus helper interfaces, icon artwork, private identity encoding, and the
second test host, documenting any acceptance environment limitation.

The [paired implementation plan](../implementation-plans/000014-linux-freedesktop-tray-plan.md)
uses number 000014 and was approved for native execution. See
[TESTBED](../TESTBED.md#linux-desktop-tray-acceptance) for verified Waybar/process/
service behavior and unverified second-host/login acceptance.

## References

- [Freedesktop Status Notifier Item specification](https://specifications.freedesktop.org/status-notifier-item/latest-single/).
- [Waybar SNI watcher implementation](https://github.com/Alexays/Waybar/blob/master/src/modules/sni/watcher.cpp).
- [Waybar SNI item and host-menu implementation](https://github.com/Alexays/Waybar/blob/master/src/modules/sni/item.cpp).
- [D-BusMenu wire interface XML](https://github.com/gnustep/libs-dbuskit/blob/master/Bundles/DBusMenu/com.canonical.dbusmenu.xml).
- [Grove daemon spec](000009-linux-daemon-design.md) and
  [systemd service spec](000010-systemd-user-service-design.md).
