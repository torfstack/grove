# macOS desktop integration

Status: approved direction, 2026-10-28. Builds on the foreground daemon in [spec 000013](000013-macos-daemon-design.md).

## Goal

Provide a macOS experience comparable to Linux: an optional per-profile launchd daemon and an independently started menu-bar companion that displays offline status and offers Refresh, Start, Stop and Quit. Quitting the companion must not stop sync. One companion targets one canonical existing profile.

## Design

Use the existing `groved` runtime and shared sync engine. Keep local watching disabled on macOS; periodic sync and optional remote polling retain the foreground daemon's behavior. Share the Linux tray presentation and coordinator where possible, with Darwin-specific AppKit and launchd adapters. Use `fyne.io/systray` v1.12.2 for the native status item and its native event loop. Bundle the companion as an unsigned, unnotarized `LSUIElement` app with Grove helpers.

Provide separate per-profile LaunchAgent templates for the daemon and companion. Store the daemon runtime plist under `~/Library/Application Support/Grove/LaunchAgents`; enabling login separately creates a private copy under `~/Library/LaunchAgents`. The controller accepts only these two fixed paths for the selected label and validates whichever path launchd reports. Installation and login start are explicit user choices. The daemon agent uses an absolute `groved` path and private absolute config path, `RunAtLoad`, no KeepAlive, and `ExitTimeOut=0`. Starting or quitting the companion does not start or stop the daemon by itself.

Service controls target only a validated `com.torfstack.groved.<instance>` agent in the current user's GUI domain. Validate the private plist, effective executable and arguments, and canonical config profile before control. Bootstrap only an unloaded verified agent with an inactive profile lease; kickstart only a verified loaded stopped agent with an inactive lease; bootout only a verified running agent with an active lease. Unknown output, inconsistent ownership, stale status, invalid files or mismatched profiles disable controls. Never use shell execution or signal arbitrary PIDs. Revalidate the current plist before Stop, then boot out the exact validated service target rather than passing a mutable plist path.

Do not install, enable or register services during build. Keep runtime data outside Git. This slice excludes FSEvents, automatic profile discovery, signing and notarization, and live Drive tests without explicit authorization.

## Acceptance

- Linux tray behavior remains intact, including per-profile identity and independent tray quit.
- Deterministic tests cover status and actions, stale state, launchctl parsing, plist privacy, wrong targets, active ownership and menu refresh/closed channels.
- Native macOS CI builds and tests the daemon and companion, runs race tests and plist lint, and verifies disposable unique-label launchd fixtures with cleanup. User desktop acceptance covers actual menu appearance, login behavior, service controls and tray-only quit.
- Document manual installation, explicit login enablement, operation, upgrade and removal. Clearly distinguish verified behavior from pending desktop acceptance.
- Preserve the existing foreground-daemon spec and decision. Record desktop-specific choices separately.
