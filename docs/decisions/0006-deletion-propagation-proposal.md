# 0006: Local deletion propagation proposal

Date: 2026-10-12. Status: accepted; implemented locally on `feat/deletion-propagation`.

## Context

After PR #9 merged, the user selected deletion propagation as the next feature.
The user confirmed the intended behavior: a local deletion may move its Drive
counterpart to Trash only if the remote item still matches Grove's saved
baseline. If Drive changed, preserve its content and surface a conflict. The
initial scope is files and folders.

## Proposal

Implement the behavior only during explicit `grove sync --two-way` on an
established profile. Keep download-only sync free of new remote mutations. Use
Drive Trash, never permanent deletion. Validate folder subtrees before trashing
a parent, and commit baseline removal only after the remote result is confirmed.
Journal the mutation for interruption recovery. Keep deletion conflicts explicit
and do not let automatic content conflict policy silently choose a side.

The accepted requirements are in
[spec 000007](../spec/000007-deletion-propagation-design.md). Implementation
follows its paired implementation plan on `feat/deletion-propagation`. One-shot
sync uses complete scans; watching belongs to later `groved` orchestration.
Interrupted cloud restoration disables fresh two-way inference until
download-only restoration finishes. Remaining conflicts stay inspectable and
explicitly resolvable during that restoration.
