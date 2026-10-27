# Grove on macOS

Grove supports macOS 13 and later. Build the local, unsigned app bundle with
`mise run build` on a Mac. This does not install the app, sign it, register
login items, or enable either service. Keep the app at a stable absolute path;
LaunchAgents do not resolve shell variables or follow app moves. The bundle
contains `grove-tray` in `Contents/MacOS` and `grove`/`groved` in `Contents/Helpers`.

Initialize a profile with `grove sync` first. Create a private daemon config
(mode 0600) outside Git, for example `~/Library/Application Support/Grove/personal.json`,
pointing at the existing profile and selecting `download` or `two-way`.
`watch_local: true` is unsupported on macOS; leave it false or omit it. Do not
store credentials or synced data inside the app bundle.

## Optional daemon at login

Copy `Contents/Helpers/groved` to a stable absolute path or keep the app bundle
fixed. Render `launchagents/com.torfstack.groved.plist.in` using the absolute
`groved` and config paths, with a unique instance label such as
`com.torfstack.groved.personal`. XML-escape substituted values. Keep
`RunAtLoad=true`, omit `KeepAlive`, and retain `ExitTimeOut=0`. Install the
rendered plist as `~/Library/LaunchAgents/<label>.plist` with mode 0600. Validate
it with `plutil -lint`, then explicitly load it with
`launchctl bootstrap gui/$(id -u) "$HOME/Library/LaunchAgents/<label>.plist"`.
The daemon runs as your user and only targets the selected existing profile.

## Optional menu bar at login

The companion is separate from the daemon. It displays one canonical profile;
quitting it leaves daemon sync running. Render
`launchagents/com.torfstack.grove-tray.plist.in` with the absolute app bundle,
profile, display name, and matching service instance. Use a unique tray label,
install it with mode 0600 under `~/Library/LaunchAgents`, validate with
`plutil -lint`, and explicitly bootstrap it with the same `gui/$(id -u)` domain.
Enabling either agent at login is optional and independent. Starting the app or
building the bundle does not enable login behavior.

Use `launchctl print gui/$(id -u)/<label>` to inspect an agent. Stop an agent
with `launchctl bootout gui/$(id -u) "$HOME/Library/LaunchAgents/<label>.plist"`.
For upgrades, stop the companion and daemon before replacing binaries, review
plist paths, then bootstrap only the agents you want. To remove integration,
boot out the selected labels and remove only their plist files; retain profile,
config, token, conflict, and recovery data unless you intentionally remove the
profile using Grove's own commands.

The app is unsigned and not notarized. Native menu-bar and LaunchAgent behavior
is verified by macOS CI; use your desktop to confirm local appearance and login
preferences before enabling agents.
