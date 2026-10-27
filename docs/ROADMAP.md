# Roadmap

Milestones are ordered proposals; detailed implementation plans come before code.

The download reliability fix in
[spec 000008](spec/000008-download-metadata-version-design.md) is merged through
PR #13 with local and full Linux live verification and clean implementation
reviews. [Spec 000009](spec/000009-linux-daemon-design.md) is implemented and
verified on `feat/linux-daemon`: one-profile periodic sync through `groved`,
a small config file, safe shutdown, and offline status. All seven plan tasks,
full local/live checks, and the independent review's regression fixes are complete.
PR #15 is merged into main at `63de937`.
[Spec 000010](spec/000010-systemd-user-service-design.md) adds a systemd
user-service template and installation instructions, merged in PR #16 at `23c583b`.
Static validation, isolated manager lifecycle checks, actual-daemon invalid-config
rejection, and existing daemon process tests pass. Dedicated-account authenticated
download acceptance through the actual user service passed six times on 2026-10-25–26;
PR #20 merged into main at `7499f6c` on 2026-10-26. Logout/boot/linger behavior
remains untested. The next broad roadmap milestone is native macOS and Windows support;
macOS one-shot sync cross-builds, but the daemon still rejects non-Linux hosts.
[approved spec 000011](spec/000011-local-watching-design.md) is implemented following its
[paired plan](implementation-plans/000011-local-watching-plan.md). Deterministic,
native filesystem, and subprocess verification pass. Independent review findings
are fixed, the initial CodeRabbit review found no actionable findings, and all
final integrated-head checks passed. PR #17 merged at `b6f2ad0` on 2026-10-24.
Dedicated-account live watcher acceptance passed on 2026-10-25 in 42.888s,
including independently verified upload in 7.50s, no-op cycles and owned cleanup.
[Spec 000012](spec/000012-remote-change-detection-design.md) implements the next
slice: opt-in account-level Drive hints with unchanged periodic scans. Offline,
process and dedicated-account remote/local live acceptance pass; final checks
and independent review findings are addressed. PR #19 merged into main at
`f6d23b5` on 2026-10-25 after final CI/security checks passed.

The current work prioritizes Linux desktop visibility:
[approved spec 000014](spec/000014-linux-freedesktop-tray-design.md) defines a
Freedesktop tray companion for Waybar and other compatible hosts. The
[paired plan](implementation-plans/000014-linux-freedesktop-tray-plan.md) covers
status, menu behavior, and optional service controls. All six implementation
slices are present; offline/race checks, actual processes, isolated Waybar
acceptance and real disposable-user-service controls pass. Final independent
review is next; a second tray host remains unverified. [Spec 000013](spec/000013-macos-daemon-design.md) remains a separate
unapproved macOS foreground-daemon proposal.

1. **Development foundation and testbed**: establish project records, design
   authentication and fixture interfaces, and implement authenticated access plus
   repeatable fixture seeding and verification.
2. **Initial download sync** (implemented; macOS live acceptance passed): download a selected fixture tree into a disposable
   local directory and verify content against the manifest. Exact safe names, empty initial destinations, and resumable profiles are
   defined in spec 000002. Prove the live fixture workflow before expanding scope.
3. **Incremental and two-way sync**: persist state, handle remote and local
   changes, and test conflicts, deletion semantics, retries, and interruption
   recovery before using valuable data. The completed slice is approved in
   [spec 000003](spec/000003-incremental-download-design.md): incremental downloads
   with local-change detection, remote additions/updates/moves, and focused
   refactoring. It is merged into main with offline verification and macOS
   incremental live acceptance. Explicit local uploads and conflict policy in
   [spec 000004](spec/000004-local-upload-design.md) are implemented with preflight
   revision checks, durable conflict candidates, CLI controls, and macOS live
   acceptance. Operator reconciliation merged in PR #8 at `24e2ebb`, following
   approved [spec 000005](spec/000005-operator-reconciliation-design.md): offline inspection,
   recovery-only retry, and verified stage export. Local offline/race verification
   passes; independent review findings were fixed and verified. Multiple preserved
   conflict candidates from [spec 000006](spec/000006-multiple-conflict-candidates-design.md)
   merged in PR #9 at `855f3c3`; force-abandon and automatic content merging remain
   deferred. Local deletion propagation from
   [spec 000007](spec/000007-deletion-propagation-design.md) merged in PR #10 at
   `8123f12`. Periodic scheduling is implemented in spec 000009; opt-in local watching is implemented in spec 000011.
4. **Linux daemon**: the first implemented slice is spec 000009: a foreground
   process periodically syncing one existing profile through the shared engine,
   with lifetime exclusion, cancellation, and offline status. Opt-in local
   watching is implemented in spec 000011; opt-in remote change hints follow
   spec 000012. Further polling optimizations remain separate work. Systemd user-service integration
   is implemented in spec 000010 with manual installation instructions.
5. **Windows and macOS**: native lifecycle integration and platform-specific
   filesystem and credential behavior, backed by tests on each platform.

Completion means demonstrated behavior and appropriate verification, not merely
the presence of commands or files.
