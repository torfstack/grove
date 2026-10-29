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

## Start the daemon now

Create private storage outside the login directory, then render the runtime
agent using your absolute app and config paths. The renderer creates mode 0600
XML and refuses to overwrite an existing file. Use an instance name containing
only letters, digits, underscores or hyphens.

```sh
mkdir -p "$HOME/Library/Application Support/Grove/LaunchAgents"
chmod 700 "$HOME/Library/Application Support/Grove/LaunchAgents"
python3 contrib/macos/render-agent.py daemon --instance personal \
  --app "/absolute/path/Grove.app" --config "/absolute/path/personal.json" \
  --output "$HOME/Library/Application Support/Grove/LaunchAgents/com.torfstack.groved.personal.plist"
plutil -lint "$HOME/Library/Application Support/Grove/LaunchAgents/com.torfstack.groved.personal.plist"
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/Application Support/Grove/LaunchAgents/com.torfstack.groved.personal.plist"
```

This starts the selected agent for the current GUI session. It does not opt in
to login start. Keep `RunAtLoad=true`, omit `KeepAlive`, and retain
`ExitTimeOut=0`. Check status using `launchctl print gui/$(id -u)/com.torfstack.groved.personal`.

## Opt into daemon start at login

To enable login start, separately render the same instance, app and config
into the login directory. This explicit file creation opts into login start;
bootout alone does not disable it for future logins.

```sh
mkdir -p "$HOME/Library/LaunchAgents"
python3 contrib/macos/render-agent.py daemon --instance personal \
  --app "/absolute/path/Grove.app" --config "/absolute/path/personal.json" \
  --output "$HOME/Library/LaunchAgents/com.torfstack.groved.personal.plist"
plutil -lint "$HOME/Library/LaunchAgents/com.torfstack.groved.personal.plist"
```

To disable future login start, first boot out the selected agent by its exact
service target, then remove only the login copy. Bootstrap the runtime plist
again if you want sync in the current session without login start.

```sh
launchctl bootout "gui/$(id -u)/com.torfstack.groved.personal"
rm "$HOME/Library/LaunchAgents/com.torfstack.groved.personal.plist"
```

## Optional menu bar at login

The companion is separate from the daemon. It displays one canonical profile;
quitting it leaves daemon sync running. For a one-session start without login
opt-in, use `open -n -a "/absolute/path/Grove.app" --args --profile-dir
"/absolute/path/profile" --name "Personal" --service-instance personal`. Render a separate tray agent with the matching instance, an absolute profile
path, and a display name:

```sh
python3 contrib/macos/render-agent.py tray --instance personal \
  --app "/absolute/path/Grove.app" --profile "/absolute/path/profile" \
  --name "Personal" --output "$HOME/Library/LaunchAgents/com.torfstack.grove-tray.personal.plist"
```

Validate it with `plutil -lint`, and explicitly bootstrap it with the same
`gui/$(id -u)` domain.
Enabling either agent at login is optional and independent. Starting the app or
building the bundle does not enable login behavior.

Use `launchctl print gui/$(id -u)/<label>` to inspect an agent. Stop an agent
with `launchctl bootout "gui/$(id -u)/<label>"`.
For upgrades, stop the companion and daemon before replacing binaries, review
plist paths, then bootstrap only the agents you want. To remove integration,
boot out the selected labels and remove only their plist files; retain profile,
config, token, conflict, and recovery data unless you intentionally remove the
profile using Grove's own commands.

The app is unsigned and not notarized. Native menu-bar and LaunchAgent behavior
requires macOS CI and desktop verification; use your desktop to confirm local appearance and login
preferences before enabling agents.
