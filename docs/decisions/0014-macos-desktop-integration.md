# 0014: Proposed macOS desktop integration

Date: 2026-10-27. Status: agreed product direction; implementation in progress.

## Context

Linux now has a shared-engine foreground daemon, optional per-profile systemd
user service, and separate Freedesktop tray companion. The user chose a full
macOS desktop experience after deferring personal Waybar acceptance to their
own desktop session.

## Agreed direction

Design macOS support around the same user-facing pieces: periodic `groved`, an
optional per-profile launchd user agent, and a native menu-bar companion for
offline status and service controls. Preserve the shared Go sync engine and
keep the daemon, indicator, and service lifecycles separate. Installation and
login enablement require explicit user action.

## Approved scope and proposed implementation

[Spec 000013](../spec/000013-macos-daemon-design.md) is the reviewable design
approved by the user on 2026-10-27. Local filesystem watching remains out of
scope; reject `watch_local: true` until a separate FSEvents design exists. The
paired implementation plan in
[000013-macos-daemon-plan.md](../implementation-plans/000013-macos-daemon-plan.md)
defines the implementation sequence and proposes macOS 13+, `fyne.io/systray`,
per-profile LaunchAgents, and a fail-closed `launchctl print` parser.
