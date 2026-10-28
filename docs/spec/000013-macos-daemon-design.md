# macOS foreground daemon

Date: 2026-10-27. Status: approved on 2026-10-28.

## Intent

Enable `groved` to run a single existing Grove profile on macOS as a foreground,
periodic process. This makes the shared daemon useful on macOS while preserving
the current simple lifecycle model and leaving native background installation
for a later slice.

## Approaches

1. **Periodic foreground daemon (recommended and selected in conversation).**
   Enable the existing periodic scheduler and shared engine on macOS. Keep
   `watch_local` disabled; retain full periodic scans. This is the smallest
   useful platform slice and requires no new dependency or OS service manager.
2. **Daemon plus native local watching and LaunchAgent.** Add FSEvents and
   per-user launchd integration at once. This gives a more automatic experience
   but combines filesystem coverage, service lifecycle, packaging, and security
   questions in one change.
3. **Continue Linux acceptance work.** Verify logout, boot, and linger behavior
   before adding another runtime target. This reduces uncertainty in Linux
   deployment, but does not advance the roadmap's next Windows/macOS milestone.

## Goals

- Permit the foreground `groved --config /absolute/path` process on macOS.
- Load daemon configuration safely, acquire per-profile single-writer
  exclusion, periodically invoke the same sync engine, persist status, and
  shut down cleanly on Ctrl-C and SIGTERM.
- Keep the existing config format and periodic behavior. If local watching is
  requested on macOS, fail with a clear actionable error rather than silently
  running without it.
- Preserve Linux behavior and its existing systemd support.
- Add deterministic platform-independent tests plus macOS-native build/runtime
  verification. Cross-build both commands for Linux as a regression check.

## Non-goals

- FSEvents or any other local filesystem watcher on macOS.
- launchd service definitions, auto-start, login/background installation, or
  packaging.
- Changes to the shared sync engine, Drive polling, credentials, or profile
  format.
- Windows runtime support.
- Claims of authenticated macOS daemon acceptance unless the dedicated test
  account and native macOS runtime are actually available and used.

## Constraints and design

- Grove remains pre-release; no earlier-release compatibility or migration is
  required. Current-run data safety and interruption recovery remain required.
- The daemon must continue sharing the `syncengine` service with `grove`.
- Keep OS-specific process signals, secure config opening, and watcher
  availability behind build-tagged boundaries.
- macOS config opening must reject symlinks and non-regular files, require
  private permissions and current-user ownership, and avoid exposing file
  contents in errors. Use APIs available in the repository's pinned Go version.
- macOS profile leases must provide cross-process, non-blocking exclusive
  ownership and a read-only active-owner probe, consistent with the existing
  Linux semantics. Verify the selected locking primitive behaves correctly on
  the supported local macOS filesystems.
- macOS `groved` handles Ctrl-C and SIGTERM and reports errors through the
  existing sanitized CLI path. It remains a foreground process.
- `watch_local: true` is rejected on macOS before daemon work starts; false or
  omitted uses the ordinary periodic scheduler. Remote change polling remains
  usable if its existing support is platform-independent and tests confirm it.
- Existing Linux build constraints and Linux-specific security checks must not
  be weakened to share implementation code.

## Acceptance

- Unit tests cover secure config validation, unsupported local watching, and
  platform process-context behavior where practical.
- Native macOS tests cover profile lease exclusion/probing and daemon process
  lifecycle with fake sync dependencies, including graceful interruption and
  recovery-safe shutdown.
- Both `grove` and `groved` build natively on macOS and cross-build for Linux.
- Required project formatting, lint, and offline Go tests pass; race tests run
  where supported. Any live Drive acceptance is explicitly gated and uses the
  dedicated test profile and fixture-owned remote data.
- Linux behavior and its CI checks remain passing.

## Open implementation choices

- Select and verify a macOS file-lock primitive compatible with the supported
  local filesystems and current lease semantics.
- Select the exact Darwin build-tag split for shared Unix and OS-specific
  helpers without widening behavior on unsupported platforms.

