# Incremental download sync

Date: 2026-10-10. Status: approved design; implementation plan awaiting review.

## Intent and agreed direction

Extend the existing one-shot shared engine so an established profile can download
remote additions and updates and follow remote renames and moves. Uploads and
remote-deletion propagation belong to later slices. Preserve local edits and
recover safely from interruption. Linux remains the acceptance target.

The user selected incremental downloads as the next milestone and requested
focused refactoring to keep the codebase maintainable, extensible, and simple.
The user approved this written spec. Behavior below is agreed for this slice;
implementation has not started. Automatic conflict resolution remains deferred.

## Approach and alternatives

Reuse complete remote scans, local content hashes, and the durable baseline.
Keep one deterministic planner and one sequential executor for initial and
incremental runs. Match remote entries by stable ID rather than only by path.

An additions-only slice would be smaller but leave ordinary edits unsupported.
A Drive changes-feed implementation could reduce listing work, but introduces
cursor recovery and membership reconciliation before correctness is established.
Full scans with additions, updates, and moves are the recommended next slice.

## Agreed behavior

- Download new files and create new folders beneath the existing binding.
- Replace a tracked file only when its local bytes still match the saved baseline.
  Validate downloaded size, checksum, and remote version before publication.
- Follow renames and moves within the selected remote root, including folders,
  only when tracked source content is unchanged and destination paths are free.
  Plan folder moves as subtree changes; do not independently move descendants
  already carried by an ancestor move.
- Retain the current strict policy for local edits, missing tracked files,
  unexpected entries, type changes, unsupported names, and filesystem collisions:
  fail preflight before executing new operations, preserving local content.
- If a previously tracked remote ID is absent from a complete scan, stop with an
  unsupported-removal error and preserve its local content. A move outside the
  selected root has the same treatment. Missing or incomplete listings never
  authorize deletion. Independent supported changes wait until removal is resolved.
- Reject rename cycles, path swaps, and reuse of an occupied tracked path in this
  slice. Report an unsupported rearrangement rather than introducing staging
  algorithms for arbitrary tree permutations.
- Keep the existing command and profile binding. An unchanged repeat performs
  no media downloads or destination writes; metadata and hash reads are allowed.

## Architecture and maintainability

Local-change detection is part of this slice. The existing durable baseline
already records file SHA-256 hashes, sizes, paths, and remote identities. Rehash
local files on each run and compare their membership, types, sizes, and hashes
with that baseline. Timestamps alone cannot prove a file is unchanged. A local
rename appears as a missing tracked path and an unexpected path; inferring local
move intent is deferred with uploads. Advance the baseline only after a verified
sync operation, never by accepting a local edit as the new downloaded content.

Keep responsibilities in the existing packages: Drive reads metadata and media;
scanners validate trees; the pure planner compares remote, local, and baseline
state; the executor applies and journals operations; the service owns resource
lifecycle and orchestration. The CLI formats sanitized outcomes.

Refactor comparison and indexing into small named helpers, using typed operation
kinds and explicit operation preconditions. Journal records should contain the
old baseline and intended result for replacements and moves, rather than adding
unrelated flags to the download-only record. Update baseline entries by remote
identity instead of appending duplicates after replacements.

Factor shared transfer verification and journal transitions only where initial
and incremental operations actually share behavior. Keep publication and move
primitives behind the existing filesystem boundary. Do not add a generic workflow
framework, a second sync engine, speculative plugins, or unrelated package moves.
Review touched functions for clarity and duplication; a line-count target is not
a substitute for understandable behavior and meaningful tests.

## Publication and recovery

Retain profile, token, destination, and ancestor exclusion. Recheck source and
destination preconditions immediately before mutation. Concurrent edits by other
applications are outside the single-writer guarantee; detected changes fail
safely, and unsupported filesystem primitives fail before mutation.

Persist intent before changing tracked paths. For replacement, stage and verify
new content and retain the old content at a journal-owned recovery path before
publishing the replacement. Record enough hashes and paths to identify old and
new content after any interruption. Do not discard the old recovery copy until
the new baseline is durable. Recovery must never overwrite a path with unexpected
content or remove a file solely because its name looks temporary.

For moves, journal source, destination, remote identity, and baseline membership
before moving. Use publication that refuses an occupied destination. Recover by
checking recorded source and destination identities and hashes; ambiguity is an
error requiring preservation, not a reason to guess ownership.

Recovery may finish an already journaled operation before planning a fresh run.
No whole-tree transaction is promised: earlier completed operations can remain
after a later failure. Every completed operation must have a recoverable durable
baseline. Failed scans cannot initiate new destination mutations.

The implementation plan must specify the exact filesystem transitions and crash
matrix before code is written, including directory moves and backup cleanup.

## State compatibility

Version the expanded journal. Accept valid version-1 profiles, including an
interrupted initial population, without asking users to redownload their tree.
Recover old pending work using its existing semantics before upgrading atomically.
Do not reinterpret malformed or unknown-version records. Preserve immutable
bindings and destination registrations. Document that older binaries cannot read
the upgraded profile; migration occurs only after validation and under its lock.

## Verification and completion

Use deterministic planner tests for additions, updates, file and folder moves,
local conflicts, removals, collisions, and unsupported rearrangements. Assert no
new destination mutations on failed preflight. Test real filesystem transitions
with failures at each journal/publication/baseline/cleanup boundary, then restart
and independently verify membership and bytes. Cover version-1 migration and
recovery, checksum failures, remote changes during transfer, and writer exclusion.

Extend the existing fake-HTTP workflow to mutate a fixture between syncs and
verify expected results independently. Reuse fixture ownership checks for any
test mutations; avoid a second fixture lifecycle or broad new user commands.
Provide an opt-in dedicated-account live scenario for additions, updates, moves,
and no-op repeats. Live execution requires explicit enablement; report Linux
runtime and live acceptance separately from cross-compilation.

For implementation, run mise formatting, lint, tests, race tests, native build,
and Linux build. Completion requires demonstrated behavior and updated product,
architecture, testbed, and status records. Uploads, deletions, conflict-copy modes,
the daemon, native documents, and shared drives remain outside this slice.

## Review and next artifact

The paired [implementation plan](../implementation-plans/000003-incremental-download-plan.md)
is written and awaits review of filesystem transitions, refactoring boundaries,
and execution method before implementation.
