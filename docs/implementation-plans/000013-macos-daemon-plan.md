# macOS Foreground Daemon Implementation Plan

**Goal:** Run the existing periodic daemon safely on macOS.

**Architecture:** Reuse the existing Unix lease and signal implementation and secure file opening on Darwin. Reject unsupported watching at configuration load before daemon work; preserve the Linux watcher and other-platform rejection.

**Tech Stack:** Go, Unix flock and secure open, existing daemon and sync engine.

**Spec:** [000013 macOS daemon](../spec/000013-macos-daemon-design.md)

## Review focus

- Symlink, non-regular, public or foreign-owned configuration must be rejected.
- Unsupported local watching must fail before acquiring a lease or performing sync.
- Concurrent processes must not share a profile; offline owner probes must work.
- SIGINT and SIGTERM must stop a real process and release ownership.
- Remote polling must remain available without local watching.

## Tasks

1. Add native Darwin tests for configuration and unsupported watching; observe failures. Share secure Unix config opening and reject watching early on Darwin. Run daemon tests.
2. Add Darwin command and process tests for platform support, signals, ownership, periodic work, and remote polling; observe failures. Share the Unix process context and enable the Darwin command. Run native process and lease tests.
3. Run formatting, lint, offline and race suites, native builds and Linux cross-builds; review changes and update status and operator documentation with verified behavior and limitations.
