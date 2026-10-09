# Roadmap

Milestones are ordered proposals; detailed implementation plans come before code.

1. **Development foundation and testbed**: establish project records, design
   authentication and fixture interfaces, and implement authenticated access plus
   repeatable fixture seeding and verification.
2. **Initial download sync**: download a selected fixture tree into a disposable
   local directory and verify content against the manifest. Define name mapping
   and existing-local-file behavior first.
3. **Incremental and two-way sync**: persist state, handle remote and local
   changes, and test conflicts, deletion semantics, retries, and interruption
   recovery before using valuable data.
4. **Linux daemon**: scheduling, watching, remote polling, profile locking,
   observable status, and systemd user service integration.
5. **Windows and macOS**: native lifecycle integration and platform-specific
   filesystem and credential behavior, backed by tests on each platform.

Completion means demonstrated behavior and appropriate verification, not merely
the presence of commands or files.
