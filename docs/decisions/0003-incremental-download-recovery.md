# 0003: Incremental downloads with preserved local conflicts

Date: 2026-10-10. Status: accepted scope and plan; implemented with offline verification.

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

## Implementation choices

The approved paired plan uses version-2 records, journal-owned replacement backups,
native no-replace moves, and narrow fixture mutation support for acceptance tests.
Version 2 retains the legacy initial-download journal shape and adds a mutually
exclusive replacement/move transaction. A stable ready-operation traversal
resolves structural dependencies without a separate graph framework. Fixture
changes accept Verified payloads, matching seed. These preserve the agreed scope
while reducing duplicated code. Incremental live acceptance remains pending.
