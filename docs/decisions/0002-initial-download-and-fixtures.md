# 0002: Initial downloads with independently verified disposable fixtures

Date: 2026-10-10. Status: accepted design; implementation details recorded below.

## Agreed decision

Implement fixture tooling and initial sync on one feature branch. Each explicitly
enabled live run seeds a new folder in a dedicated disposable account through the
Drive API, downloads it through the shared engine, independently verifies the
committed manifest, and repeats without redundant transfers. Ordinary files and
folders are the initial scope; native documents, shortcuts, and duplicate sibling
names are deferred. The user approved spec 000002 and its plan for native execution.

Sync starts from an empty destination and never overwrites or deletes content.
Persist verified progress so interrupted downloads can restart at file boundaries.
After initial completion, remote changes and local edits are errors pending a
separate incremental-sync design.

## Implementation choices

Use standard-library HTTP for the narrow Drive v3 adapter. Expose an injectable
sync service while retaining a production-default Run entry point; this allows
HTTP integration testing without environment-controlled alternate API endpoints.
Use one Unix boundary for rooted hard-link publication on Linux/macOS. Persistent
destination registrations retain profile ownership until a future reset command,
preventing silent reclamation that could admit concurrent writers.

These are implementation choices within the approved design, not separate user
requests. They add a service injection boundary and retain private registry state.
Optional live cancellation is deferred; deterministic cancellation and journal
failure recovery are tested offline. Real Drive acceptance remains pending explicit
local authentication and live-test enablement.
