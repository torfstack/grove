# Grove Linux tray

`grove-tray` shows one existing profile through the Freedesktop
StatusNotifierItem/D-BusMenu protocols. It works with compatible tray hosts;
Waybar is the primary acceptance target. Other desktops may need a tray host
or extension. The host controls icon placement. Linux and a graphical session
D-Bus are required; GTK/Qt, a particular icon font, and systemd are optional.

Build with `mise run build`, then launch using your actual absolute profile path:

```sh
./bin/grove-tray --profile-dir /absolute/path/to/profile --name Personal
```

The profile must already exist and work with Grove. The companion reads offline
status without credentials or network access. One companion can run per canonical
profile on each session bus; launch separate processes for additional profiles.
Names are public labels, limited to 80 characters, never filenames or secrets.
Do not put profiles/config/tokens inside a Git checkout or synced destination.

The icon distinguishes running/waiting, last-recorded syncing, stopped, and
attention. Tooltip/menu details distinguish observed daemon ownership from
historical attempts. `Last sync completed` does not promise current remote
synchronization. Busy/unavailable observations never invent zero conflicts;
retained conflict/recovery facts are explicitly last known. Refresh only rereads
status. The icon remains visible while the daemon is stopped, subject to the
panel's visibility preferences. Quit tray or logging out closes only the
indicator; it does not stop the daemon or cancel an accepted service job.

## Waybar

Add `tray` to the right-hand module list in your existing Waybar configuration;
retain all your other modules. For example:

```json
{
  "modules-right": ["tray", "clock"],
  "tray": {"icon-size": 20, "spacing": 8}
}
```

Follow your panel's usual reload procedure. Starting the companion before the
panel is supported; it waits for the host and registers again after bar restarts.
A missing session bus is a startup error. A disconnected bus ends the companion
with a failure so your session supervisor can relaunch it. No legacy XEmbed
fallback is supplied.

## Optional user-service controls

If you use the [Grove systemd template](../systemd/README.md), opt into controls:

```sh
./bin/grove-tray --profile-dir /absolute/path/to/profile --name Personal --systemd-instance personal
```

This targets only `groved@personal.service` in your user manager. It never
installs/enables a service, changes lingering, or starts one automatically.
Before enabling controls and before each action, the companion validates the
unit's effective direct `groved --config ABSOLUTE_PATH` invocation and private
config against the selected profile. Custom config paths and `--config=PATH`
work; wrapper scripts, additional arguments/commands, aliases to other units,
or mismatching/unsafe configs disable controls. Instance labels use only ASCII
letters, digits, underscores and hyphens (maximum 128 characters).

A foreground daemon can own the profile while this unit is inactive. Status
shows these separately and disables start over an active/unknown owner. Start
and stop target the named service, not a PID. Requests use systemd's `fail` job
mode, so conflicting queued jobs are not replaced. Accepted start is not proof
of a running profile or successful sync. Long graceful stops remain stopping;
the tray supplies no force-kill timeout. Unknown/denied control outcomes remain
visible and require fresh service observation before another request.

Conflict resolution, recovery, authentication, sync-mode changes and an immediate
sync action remain in the CLI. The menu supplies status, refresh, optional
service start/stop, and Quit tray; it does not open a terminal or an application
window.

## Install and session autostart

Install the binary to an absolute location of your choice. Include
[third-party notices](../../THIRD_PARTY_NOTICES.md) with binary distributions.
Named icons are optional because the binary embeds pixmap fallbacks. To install
the provided scalable named icons, run from the source checkout:

```sh
(
  set -eu
  grove_icon_dir="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/scalable/apps"
  mkdir -p "$grove_icon_dir"
  for grove_icon in internal/tray/icons/*.svg; do
    install -m 644 "$grove_icon" "$grove_icon_dir/"
  done
)
```

The inert [desktop-entry template](grove-tray.desktop.in) is for manual use.
Copy it to a private temporary file and replace the executable/profile/name
placeholders. Add `--systemd-instance personal` only if you want service controls.
Review the resulting entry, then install it as
`${XDG_CONFIG_HOME:-$HOME/.config}/autostart/grove-tray-personal.desktop`.
Use a different filename for each profile and avoid overwriting existing entries.
Removing that specific entry disables future session autostart; no daemon or
profile data is removed. You can alternatively invoke the same explicit command
from your compositor/session configuration.

Desktop Entry `Exec` is not a shell command: `~`, `$HOME`, pipes, and command
substitution are not expanded. Use absolute paths, quote each argument containing
spaces, and encode a literal percent as `%%` because percent introduces desktop
field codes. Desktop Entry string escaping happens before Exec parsing: a literal
backslash inside a quoted argument requires four backslashes in the file, and a
literal double quote requires an escaped backslash followed by the quote
(`\\"`). Consult the
[Desktop Entry Exec rules](https://specifications.freedesktop.org/desktop-entry-spec/latest/exec-variables.html)
for unusual paths; do not copy shell quoting blindly.

## Verification

Offline Linux tests require `dbus-daemon` (for example, the distro's `dbus-daemon`
or `dbus` package). Tests create private session buses and never use your normal
tray or ordinary services. Desktop and real-user-manager acceptance are separate
checks recorded in [TESTBED](../../docs/TESTBED.md); do not treat an unavailable
host check as proven compatibility. Live Drive access is unnecessary to verify
tray behavior.
