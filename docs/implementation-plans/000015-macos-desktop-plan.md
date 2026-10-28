# macOS desktop integration plan

**Spec:** [000015 macOS desktop integration](../spec/000015-macos-desktop-design.md). Base: `origin/main` at `e326049`, which already includes the macOS foreground daemon. The closed `design/macos-desktop-integration` branch is a reference, not a merge source; port and review each relevant change against main.

## Review focus

- A mismatched, changed or unverified launchd job or plist must never enable a control command.
- Stop requires both a running selected job and active profile ownership; Start requires inactive ownership and fresh valid profile status.
- The AppKit event loop must run on the main thread; removed menu items must not dispatch actions or leave listeners behind.
- Per-profile identity must prevent duplicate companions without conflicting with the daemon lease.
- Native tests must use unique owned launchd labels and remove only their fixtures; do not use live Drive data.

## Tasks

1. **Share the tray model and coordinator.** Port the platform-neutral presentation, options and lifecycle boundaries from the preserved branch. Keep Linux transport and service behavior; retain the generic `--service-instance` flag with updated Linux docs. Run Linux tray and command tests, including race checks.
2. **Implement launchd control.** Port and review the parser, private plist validation, command runner and controller against current daemon configuration. Test captured output, unknown layouts, wrong profile/arguments, altered plists, ownership mismatches, unloaded Bootstrap, loaded Kickstart and running Bootout. Add the daemon agent template.
3. **Implement the native menu bar.** Port the Darwin adapter and shared command, using `systray.Run` to drive AppKit on the main thread. Test item mapping, enablement, dynamic refresh, closed channels, actions, shutdown and duplicate identity. Include the dependency notice. Run native macOS and Linux tray tests.
4. **Package and document.** Port the app build script, metadata and companion agent template; make `mise run build` produce the bundle on Darwin and preserve Linux builds. Document explicit install, login opt-in, start, stop, upgrade and removal. Check rendered plist paths and XML escaping; build must not register services.
5. **Verify and integrate.** Add native macOS CI with pinned tools, offline/race tests, plist lint, native app build and Linux cross-builds. Run unique-label disposable LaunchAgent tests, full Linux checks and macOS checks. Record actual results and remaining interactive desktop/login limits in product, architecture, roadmap, testbed and status. Obtain one whole-change review and the required initial CodeRabbit review; address valid findings and wait for fresh CI before merging under the user's standing authorization.

For each task, add or adapt meaningful tests before behavior changes, verify the affected packages, update the progress record and commit. Preserve main's daemon implementation and spec 000013. If native launchd or interactive desktop checks cannot run on the available host, record the exact limitation and do not claim those acceptance items passed.
