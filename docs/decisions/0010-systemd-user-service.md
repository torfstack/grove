# 0010: Distribute a systemd user-service template

Date: 2026-10-24. Status: accepted by the user; implemented.

The user selected a service template and instructions over CLI installation
commands, then approved [spec 000010](../spec/000010-systemd-user-service-design.md).
Ship `contrib/systemd/groved@.service` for manual user installation now and
conventional distribution packaging later. No package or automatic installer
is introduced in this slice.

Each named instance uses `%h/.config/grove/daemons/%i.json`. Literal `%i` avoids
unescaping instance labels into directory separators. The fixed home-relative
convention is independent of XDG_CONFIG_HOME; per-instance command overrides
support other paths. The vendor command targets `/usr/bin/groved`; source builds
use an explicit `~/.local/bin/groved` override without relying on shell PATH.

Use `Type=exec`, private umask, journal output, and `default.target` activation.
Do not automatically enable services or lingering. `Restart=no` leaves fatal
configuration, ownership, persistence, cleanup, and unexpected-exit failures
visible for inspection; ordinary attempt failures already retry in the daemon.
This costs automatic recovery from an otherwise harmless process crash.

SIGTERM with `TimeoutStopSec=infinity` preserves the existing wait-for-cleanup
contract rather than introducing timeout-driven SIGKILL. Consequently stop,
restart, or user-manager shutdown may wait indefinitely for stuck I/O. Forced
termination retains the engine's normal durable recovery obligations. Arbitrary
configured profile, token, and destination paths remain accessible without
additional filesystem sandboxing. Watching, hot reload, platform lifecycle
support outside Linux, and package-manager integration remain separate work.

See [TESTBED](../TESTBED.md#systemd-user-service-acceptance) for the boundary
between helper-manager acceptance, actual-daemon startup rejection, and existing
real-daemon process tests. Authenticated sync under systemd was not exercised.
