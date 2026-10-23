# Roadmap

Milestones are ordered proposals; detailed implementation plans come before code.

The download reliability fix in
[spec 000008](spec/000008-download-metadata-version-design.md) is merged through
PR #13 with local and full Linux live verification and clean implementation
reviews. The next ticket is approved
[spec 000009](spec/000009-linux-daemon-design.md): one-profile periodic sync
through `groved`, a small config file, safe shutdown, and offline status. Its
paired implementation plan is prepared for review before native execution.

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
   acceptance. Operator reconciliation is implemented on
   `feat/operator-reconciliation`, following approved
   [spec 000005](spec/000005-operator-reconciliation-design.md): offline inspection,
   recovery-only retry, and verified stage export. Local offline/race verification
   passes; independent review findings were fixed and verified. Multi-competitor
   UI recovery, force-abandon, and automatic content merging follow later. Local
   deletion propagation is approved in
   [spec 000007](spec/000007-deletion-propagation-design.md) and is implemented
   on `feat/deletion-propagation`, pending pull request review. Filesystem
   watching and scheduled continuous sync remain part of the later `groved`
   daemon milestone.
4. **Linux daemon**: the first proposed slice is spec 000009: a foreground
   process periodically syncing one existing profile through the shared engine,
   with lifetime exclusion, cancellation, and offline status. Local file
   watching, richer remote polling, and systemd user service integration follow.
5. **Windows and macOS**: native lifecycle integration and platform-specific
   filesystem and credential behavior, backed by tests on each platform.

Completion means demonstrated behavior and appropriate verification, not merely
the presence of commands or files.
