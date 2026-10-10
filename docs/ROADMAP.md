# Roadmap

Milestones are ordered proposals; detailed implementation plans come before code.

1. **Development foundation and testbed**: establish project records, design
   authentication and fixture interfaces, and implement authenticated access plus
   repeatable fixture seeding and verification.
2. **Initial download sync** (implemented; macOS live acceptance passed): download a selected fixture tree into a disposable
   local directory and verify content against the manifest. Exact safe names, empty initial destinations, and resumable profiles are
   defined in spec 000002. Prove the live fixture workflow before expanding scope.
3. **Incremental and two-way sync**: persist state, handle remote and local
   changes, and test conflicts, deletion semantics, retries, and interruption
   recovery before using valuable data. The next slice is approved in
   [spec 000003](spec/000003-incremental-download-design.md): incremental downloads
   with local-change detection, remote additions/updates/moves, and focused
   refactoring. Its implementation plan awaits review; uploads, deletion
   propagation, and automatic conflict resolution follow later.
4. **Linux daemon**: scheduling, watching, remote polling, profile locking,
   observable status, and systemd user service integration.
5. **Windows and macOS**: native lifecycle integration and platform-specific
   filesystem and credential behavior, backed by tests on each platform.

Completion means demonstrated behavior and appropriate verification, not merely
the presence of commands or files.
