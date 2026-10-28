# 0014: macOS foreground daemon

Status: agreed, 2026-10-28.

Extend the existing foreground periodic daemon to macOS as described in
[spec 000013](../spec/000013-macos-daemon-design.md). Reuse Unix `flock` profile
leases and the existing secure file-opening and signal handling boundaries.
Local watching remains Linux-only and an enabled `watch_local` is rejected
before daemon work on macOS. Remote polling remains optional. LaunchAgent
integration, macOS filesystem watching, packaging and Windows are separate
work. Preserve the shared engine and Linux security checks.
