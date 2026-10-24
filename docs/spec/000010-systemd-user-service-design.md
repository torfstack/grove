# Systemd user-service integration

Date: 2026-10-24. Status: approved by the user; implementation pending.

Independent builder check: "nothing important left"; no consequential unanswered
questions were identified.

## Intent and scope

Make the existing Linux daemon convenient to run as a background user service.
The user selected a distributed service template and installation instructions,
rather than adding service-management commands to Grove. PR #15 is merged at
`63de937`; periodic sync, lifetime exclusion, offline status, and graceful
cancellation already exist under spec 000009.

Ship one reusable unit at `contrib/systemd/groved@.service`, plus operator
documentation. No Go behavior changes, package-manager integration, D-Bus
activation, file watching, timer unit, or automatic service installation are
included. Do not install or enable a persistent service on the developer's
account as part of implementation acceptance.

## Approaches

- **Template and instructions (selected):** users copy a provided unit into
  their user-unit directory and use standard systemctl commands. Future distro
  packages can install the same template into `/usr/lib/systemd/user/`.
- **CLI installation commands:** generate/install/remove units from Grove.
  This adds path handling and service-manager operations that are unnecessary
  for the current slice.
- **Instructions alone:** users compose their own units. This avoids an asset
  but loses a shared, verifiable lifecycle configuration.

## Approved service contract

The user approved the following recommended details in the written design.
Each instance runs one existing daemon config: `groved@personal.service`
uses `%h/.config/grove/daemons/%i.json`. Use literal `%i`, not unescaped `%I`,
so an instance name cannot introduce path separators. Document simple instance
names using letters, digits, underscores, and hyphens; names are public labels,
not credentials. The JSON retains its existing explicit mode, absolute profile
path, interval, and owner-only permissions. No profile or config is created by
the service.

The distributed unit uses `/usr/bin/groved` as its executable for conventional
future packaging. Source-build instructions install the binary into
`~/.local/bin/groved` and provide an ExecStart drop-in that clears the original
command before replacing it with the quoted `%h/.local/bin/groved` path and
quoted config argument. No shell wrapper or interactive shell PATH is used.
Document systemd escaping for literal percent signs and other custom paths;
recommend the fixed paths above for the initial walkthrough. Config lookup is
deliberately fixed beneath `%h/.config`, independent of XDG_CONFIG_HOME; explain
how an instance drop-in selects an arbitrary absolute config path instead.

Use `Type=exec`, `UMask=0077`, explicit journal output, and
`WantedBy=default.target`. No User= directive or root service is needed.
Type=exec confirms execution, not successful profile validation or sync;
operators check both systemctl state and `grove status`.

Use `Restart=no` explicitly. Ordinary attempt errors already wait and retry
inside groved. Fatal config, ownership, status-persistence, and cleanup errors
must remain visible for operator inspection rather than loop automatically.
Unexpected process death also requires an explicit restart, which uses normal
durable engine recovery. No readiness notification or ExecReload is introduced.

Use SIGTERM and `TimeoutStopSec=infinity`: stopping waits for cancellation and
cleanup, preserving the daemon contract without systemd's usual timeout-driven
SIGKILL. Document that stop/restart may wait indefinitely if I/O will not return,
and that forced termination can leave historical status and recovery work.
Keep normal control-group termination behavior; no background child survives
service shutdown. Avoid filesystem sandbox directives that could hide arbitrary
configured profile, destination, or token paths. No network-online dependency:
the existing periodic loop handles unavailable networking.

## Operator documentation

Document prerequisites: Linux with a running systemd user manager, built or
installed binaries, successful authentication and initial profile sync, and
private config outside Git and the synced destination. Build tools are not
invoked by the unit. Installation examples must preserve existing configs and
drop-ins rather than silently overwrite them.

Cover copying the template, installing a source-built binary, creating the
drop-in and private config, daemon-reload, start, enable for future user-manager
starts, status, journal inspection, stop, restart, disable, and removal. Enable
only the chosen instance. Explain that config changes require restart, unit
changes require daemon-reload, and a copied template must be updated manually
after upgrades. Removal stops/disables the instance before removing its files;
profiles, tokens, sync data, and daemon JSON are retained.

Login-session behavior and optional `loginctl enable-linger` are documented
separately. Lingering may require administrator permission, starts the user
manager at boot, and keeps it alive after logout; it does not unlock encrypted
home directories or guarantee configured mounts are available. Do not enable it
automatically. Include its inverse and explain that disabling linger affects
other user services too.

## Verification and acceptance

Validate the shipped template and documented source-build drop-in with
systemd-analyze verify, accounting for the package executable's absence during
source development. Verify expansion into one config argument, including home
paths with spaces, and report any unavailable runtime checks explicitly.

Where the user manager is available, use isolated temporary runtime units and
disposable local state to verify execution, journal output, failure visibility,
absence of automatic restart, graceful SIGTERM/stop, and cleanup of test units.
Use a harmless temporary process when testing manager mechanics; label that
evidence separately from real-groved acceptance. Verify real groved startup
failure using a disposable invalid config, without credentials or Drive access.
Existing process tests cover real daemon cancellation and profile exclusion;
reuse them where relevant rather than adding tests that merely match unit text.
Do not mutate normal account units, enable lingering, or access live tokens.
Any authenticated systemd acceptance remains opt-in with dedicated fixtures.

Update README, architecture, product, roadmap, and status to distinguish shipped
integration from verified runtime behavior. Record actual commands and limits.
Implementation follows a paired plan after approval of this design.

## References

- [systemd.service](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html)
- [systemd.unit](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html)
- [Arch Ghostty package files](https://archlinux.org/packages/extra/x86_64/ghostty/files/)
