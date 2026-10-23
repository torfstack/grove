# 0008: First Linux daemon slice

Date: 2026-10-23. Status: accepted; implemented, final acceptance in progress.

## Agreed direction

After PR #13 merged, the user selected periodic background sync for one existing
profile and chose a small config file instead of repeated flags. Decision 0001
already requires a Go daemon that directly shares the CLI's sync engine.

## Agreed design

[Spec 000009](../spec/000009-linux-daemon-design.md) defines a foreground
`groved --config PATH`, a private JSON config referencing an existing profile,
an explicit download/two-way mode, immediate startup sync, and completion-based
periodic scheduling. Default to a five-minute interval, hold one lifetime daemon
lease per profile, and preserve per-attempt exclusion for manual commands.
Add offline `grove status` using lock-observed ownership and advisory historical
records; never derive liveness or mutation authority from a stale PID/cache.

The user approved the written spec; its paired plan is in native execution. Watching,
systemd installation, multiple profiles in one process, IPC, and hot reload
remain later slices. Existing-profile resolution must remain inside the shared
engine's locking boundary so the scheduler cannot accidentally create or rebind
a profile after a race. Existing sync/recovery safety rules remain authoritative.

## Implementation choices

Native execution continued in the user's dedicated feature branch in the existing
checkout, preserving the approved documents and workflow. A separate worktree
would provide stronger isolation from simultaneous edits.

The advisory writer shares the existing atomic JSON implementation through a
new `WriteExistingJSON` entrypoint that never creates its parent. This prevents
status from recreating a missing profile, at the cost of one narrow shared API.

Real signal tests required private Drive errors to preserve transport/body
cancellation identity, including the context's exact signal cancellation cause.
Displayed errors remain fixed; unrelated joined cleanup failures remain fatal.
This changes internal error-unwrapping semantics and is covered by cancellation
and redaction regressions across reads, folder creation, pinning, and uploads.
