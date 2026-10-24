# Run Grove with a systemd user service

The [groved@.service](groved@.service) template runs one existing profile per
instance, as your normal user. `groved@personal.service` reads
`~/.config/grove/daemons/personal.json`. Use simple instance labels containing
letters, digits, underscores, and hyphens; labels are visible in service listings.
Two instances pointing at the same profile still cannot run concurrently.

You need Linux with a running systemd user manager, authenticated Grove access,
and an existing profile initialized with `grove sync`. First verify the profile
works with a foreground daemon, as described in the
[main guide](../../README.md#periodic-sync-on-linux). The service uses the saved
profile/token/destination binding; it never initializes a profile or opens a
login browser. Keep config, profile, and token files outside Git and the synced
destination. The daemon config must be an owner-only regular file.

## Install from source

Run `mise run build` from the repository root. The unit does not run mise or
depend on your interactive shell's PATH. This first-install example uses
`~/.local/bin/groved` and a per-instance command override. It intentionally
refuses existing files, including symlinks: review an existing setup using
`systemctl --user cat groved@personal.service` and follow the upgrade section.

User units go beneath `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user`, matching
the user manager's configuration environment. Check the running manager with
`systemctl --user show -p UnitPath --value`. The shell-derived `grove_unit_dir`
below must match a directory in that output; align the shell's XDG_CONFIG_HOME
with the manager before installing if they differ. For a newly created directory,
run daemon-reload and check again before starting the service. Unlike this
manager query, `systemd-analyze --user unit-paths` does not contact the running
manager. The unit directory is separate from the fixed Grove JSON path.

Run this block from the repository root in a POSIX shell. Replace `personal`
consistently if you use a different label. The initial config is download-only;
edit the absolute profile path before starting.

```sh
(
  set -eu
  grove_unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  for target in \
    "$HOME/.local/bin/groved" \
    "$grove_unit_dir/groved@.service" \
    "$grove_unit_dir/groved@personal.service.d/override.conf" \
    "$HOME/.config/grove/daemons/personal.json"
  do
    if [ -e "$target" ] || [ -L "$target" ]; then
      echo "Existing file: $target; review it before installing." >&2
      exit 1
    fi
  done
  mkdir -p "$HOME/.local/bin" "$grove_unit_dir/groved@personal.service.d"
  install -m 755 bin/groved "$HOME/.local/bin/groved"
  install -m 644 contrib/systemd/groved@.service "$grove_unit_dir/groved@.service"
  umask 077
  mkdir -p "$HOME/.config/grove/daemons"
  chmod 700 "$HOME/.config/grove/daemons"
  set -C
  cat > "$grove_unit_dir/groved@personal.service.d/override.conf" <<'UNIT'
[Service]
ExecStart=
ExecStart="%h/.local/bin/groved" --config "%h/.config/grove/daemons/%i.json"
UNIT
  cat > "$HOME/.config/grove/daemons/personal.json" <<'JSON'
{
  "version": 1,
  "profile_dir": "/home/you/.local/state/grove/profiles/personal",
  "mode": "download",
  "interval": "5m",
  "watch_local": false
}
JSON
)
```

Set `watch_local` to true for debounced native local events alongside periodic
scans. The destination must already exist and `/proc/self/fd` must be available.
Watcher/root/coverage failures stop visibly under the same `Restart=no` policy;
inspect the journal, address the cause, then restart. See the
[daemon guide](../../README.md#periodic-sync-on-linux) for debounce, retry, and
filesystem limits. Changing this flag requires restarting the instance.

Edit `~/.config/grove/daemons/personal.json` in your editor to set the real,
absolute profile path. `mode` is required. Choose `two-way` only when you intend
to enable uploads and local-deletion propagation on a fully populated profile
with write authorization. Leave the config at mode 0600.

The template intentionally uses `%h/.config`, independent of XDG_CONFIG_HOME.
For nonstandard config paths, edit the per-instance override and replace the
config argument with your absolute path. Unit commands are parsed by systemd,
not a shell: no `~`, command substitution, or shell variable expansion. Quote
each path; literal percent and dollar signs in custom paths must be written
`%%` and `$$`. Backslashes and quotes require systemd escaping. `%h` supplies
the user's home and `%i` the literal escaped instance label; `%I` would unescape
the label and is deliberately unused. See
[command-line syntax](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#Command%20lines)
and [unit specifiers](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html#Specifiers).

## Package installation

The shipped template targets `/usr/bin/groved`. A future distribution package
can install it at `/usr/lib/systemd/user/groved@.service` alongside that binary;
this repository does not yet provide such a package. With a package-provided
unit, omit the source-build copy and ExecStart override. Create only the private
daemon config, then use the same commands below. Personal units and drop-ins
under `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/` take precedence over vendor
files; remove a
source-build override/copy deliberately when switching to packaged binaries.

## Start and inspect

```sh
systemctl --user daemon-reload
systemctl --user start groved@personal.service
systemctl --user status groved@personal.service
journalctl --user -u groved@personal.service -n 50 --no-pager
./bin/grove status --profile-dir /absolute/path/to/profile
```

Use your installed `grove` instead of `./bin/grove` when outside the checkout.
`Type=exec` means the executable was launched; it does not establish valid
configuration, successful sync, or readiness. Check service state after startup
and use `grove status` for ownership, historical attempts, counts, conflicts, and
pending recovery. The journal contains process lifecycle and sanitized fatal
errors; groved does not emit a per-attempt sync log. Ordinary sync/auth/busy
failures remain in the daemon's periodic loop and appear in offline status.

Enable the chosen instance for future user-manager starts, separately from
starting it now:

```sh
systemctl --user enable groved@personal.service
```

The unit uses `default.target`; enabling does not enable lingering. It has no
network-online dependency. Unavailable networking is handled by later attempts.
Profile, token, config, and destination paths must be accessible to this user.

## Stop, repair, and upgrade

```sh
systemctl --user stop groved@personal.service
# Inspect conflicts/recovery or reauthenticate with Grove while stopped.
systemctl --user start groved@personal.service
```

Stop sends SIGTERM and waits for cancellation and cleanup. `TimeoutStopSec=infinity`
prevents a normal stop from escalating to a timeout-driven SIGKILL. Stop,
restart, and user-manager shutdown can wait indefinitely if I/O never returns.
Forced termination may leave stale historical status and durable recovery work;
inspect with Grove's existing status/conflict/recovery commands before restarting.
Never discard an uncertain journal merely because the process stopped.

Fatal configuration, ownership, persistence, and cleanup errors remain stopped
with failed service status. `Restart=no` also leaves unexpected process death
stopped. Inspect the failure, repair its cause, and explicitly start the service;
an ordinary attempt failure already has the daemon's normal periodic retry.

After editing daemon JSON, apply it with:

```sh
systemctl --user restart groved@personal.service
```

There is no reload command. After editing a unit or drop-in, run
`systemctl --user daemon-reload` before restarting. To upgrade a source build,
stop every instance using the binary, rebuild, and deliberately replace
`~/.local/bin/groved` using `install -m 755 bin/groved "$HOME/.local/bin/groved"`.
Review upstream template changes against your copy and local overrides before
replacing the copied template. Reload the manager and start the desired
instances. Copied files do not update automatically; package vendor files do
update through their package manager.

To disable and remove this instance:

```sh
systemctl --user disable --now groved@personal.service
grove_unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
# Review and remove only this instance's override, if you installed it:
rm -i "$grove_unit_dir/groved@personal.service.d/override.conf"
rmdir "$grove_unit_dir/groved@personal.service.d"
systemctl --user daemon-reload
```

Once no instances use a manually copied template or binary, you can remove
those copies too, then run daemon-reload again. Do not remove vendor files
manually. Keep profiles, tokens, daemon JSON, and synced data; service removal
does not require deleting them.

## Optional operation after logout

Without lingering, the service follows your user-manager/session lifecycle.
To start the manager at boot and keep it after logout, explicitly opt in:

```sh
loginctl enable-linger
```

This may require administrator permission. It does not unlock encrypted home
directories or ensure custom mounts are available. Review those requirements
before enabling boot-time sync. Undo with `loginctl disable-linger`; this affects
the lifecycle of all your user services, not only Grove. See
[loginctl](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html#enable-linger%20%5BUSER%E2%80%A6%5D).
