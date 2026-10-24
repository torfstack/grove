# 0011: Opt-in native Linux local watching

Date: 2026-10-24. Status: accepted by the user; implemented and locally verified.

The user approved [spec 000011](../spec/000011-local-watching-design.md) and its
paired implementation plan after systemd integration merged. Add strict boolean
`watch_local`, default false, to request debounced normal sync attempts from
native events. Keep periodic remote reconciliation and the existing shared engine,
locks, mode, conflict, revision, and deletion safety rules. Events are hints only.

The approved contract uses a one-second quiet period capped at five seconds,
independent periodic deadlines, interval-limited retries after failed/busy work,
and one engine attempt at a time. Watch setup/coverage/root failures stop visibly;
there is no silent downgrade of requested watching. The event reader stays active
during sync and repair, with bounded pending hints. Own writes may cause a no-op
follow-up; read-only scans must settle. Shutdown joins all cleanup before releasing
lifetime ownership. Native events remain best effort, with full scans required.

Implementation choices: use existing pinned x/sys inotify without a new library;
anchor traversal and registration through directory descriptors and /proc/self/fd,
which becomes a documented Linux prerequisite. Avoid descriptor-to-path event
mapping and reconcile coverage on structural/overflow hints. Keep selectable
TimerClock as an extension of the existing Clock interface, preserving periodic-
only tests and behavior. Add historical watch_local to version-1 records/status;
no migration is needed under pre-release policy. Eight unsuccessful membership
repair passes stop rather than claiming complete coverage.

Real process tests exposed normal probe-directory disappearance during traversal.
ENOENT from enumeration now retries coverage instead of treating that race as a
terminal fault. Unrecoverable permission/resource/root errors still stop visibly.
