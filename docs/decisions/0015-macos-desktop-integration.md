# 0015: macOS desktop integration

Status: agreed direction, 2026-10-28.

Build an optional per-profile launchd daemon service and independent native menu-bar companion on the foreground macOS daemon merged in PR #23. Reuse shared tray presentation and coordinator logic where safe. Follow [spec 000015](../spec/000015-macos-desktop-design.md) and its [implementation plan](../implementation-plans/000015-macos-desktop-plan.md). Installation and login start are explicit. Local watching, signing, notarization and automatic profile discovery remain outside this slice. The closed PR #22 is a reference for porting, not a merge source.

Daemon runtime plists live under `~/Library/Application Support/Grove/LaunchAgents`; a separate private copy under `~/Library/LaunchAgents` opts into login start. Loaded jobs from either fixed location are verified against their own plist. Stop uses the exact validated launchd service target.
