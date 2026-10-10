# 0003: Incremental downloads with preserved local conflicts

Date: 2026-10-10. Status: accepted scope; implementation plan awaiting review.

## Agreed decision

The user approved [spec 000003](../spec/000003-incremental-download-design.md).
Extend the existing shared engine with remote additions, updates, and safe moves.
Use persisted content hashes and complete local/remote scans to detect changes.
Local conflicts fail preflight and preserve content; choosing a winner, merging,
uploads, and deletion propagation are deferred. Missing remote objects never
authorize deletion. Focus refactoring on keeping this engine simple and maintainable.

## Consequences

An established profile gains incremental downloads without a second engine.
Strict conflicts/removals block independent new work until resolved. Replacements
and moves require versioned durable intent, verified content, and deterministic
restart handling; existing version-1 profiles must remain migratable.

## Plan proposals

The paired plan proposes version-2 records, journal-owned replacement backups,
native no-replace moves, and narrow fixture mutation support for acceptance tests.
These implementation choices await plan review. Execution has not started.
