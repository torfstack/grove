# Multiple preserved conflict candidates

Date: 2026-10-12. Status: implemented; merged in PR #9 at `855f3c3`.
The intent below records the original pre-implementation context.

## Intent

Make every competing Drive revision Grove has preserved during an upload race
available for deliberate operator resolution. Today Grove captures one
competitor as a normal pending conflict. When a complete revision scan finds
more than one possible competitor, Grove pins and records their identities but
leaves the upload transaction blocked because the conflict model exposes only a
single cloud candidate.

The user selected this as the next design task after merging PR #8. Linux is the
primary platform; design and deterministic implementation work can run on the
current macOS machine, with Linux runtime acceptance later. Grove remains
pre-release, so migration from earlier releases is not required. Existing current
run data must remain safe through interruption.

## Success criteria

- A complete, anchored revision scan with multiple possible competitors can
  become a durable pending conflict after every candidate is independently
  captured and verified.
- The user can inspect candidate descriptions and select the local version or
  one preserved cloud revision.
- Resolution revalidates the live remote object and verifies every stage before
  any replacement or upload. Missing, changed, or unverifiable candidates leave
  the conflict and all other candidates intact.
- Interruptions while pinning, capturing, publishing, or resolving resume without
  losing candidate identities or deleting stages still referenced by the
  conflict.
- Existing single-cloud-candidate conflicts keep their current behavior.

## Proposed behavior

### Candidate model

Represent a pending conflict as one immutable local candidate plus an ordered
set of cloud candidates. Each cloud candidate records its Drive revision ID,
revision timestamp when available, MD5, size, SHA-256, private stage path, and
stage file identity. The current remote head remains separately recorded as the
live object against which resolution is checked; it is not silently treated as
one of the historical competitors. Candidate order is stable, based on the
revision scan order normalized by timestamp and revision identity, so a displayed
ordinal continues to refer to the same candidate while the conflict is pending.

Candidate metadata and stages are published to the profile state only after all
identified competitors have been pinned, downloaded, verified against Drive
metadata, and durably recorded. If capture fails partway through, retain the
transaction and every recorded identity, pin, and verified stage. Retry must
resume or reassess the same obligations; a candidate disappearing from a later
listing does not prove it was safely handled. Never replace a known candidate
stage with bytes from a different revision.

The capture journal must make progress durable before each remote pin or
download, identify stage ownership before writing bytes, and commit the completed
candidate set atomically with transaction retirement. Preserve the existing
preflight and current-head anchors, timestamp boundary, pin quota handling, and
unknown-ownership behavior. Do not loosen the accepted rule that an incomplete
revision listing cannot authorize a baseline commit.

### Inspection and resolution

Extend the existing `grove conflicts list` output to include the number of cloud
candidates. Add a detail command (proposed: `grove conflicts show <id>`) that
prints a stable ordinal, source (`local` or `cloud`), timestamp when known, and
size for each candidate. Do not print credentials, generated operation markers,
private stage paths, hashes, or raw Drive revision IDs. Keep the existing local
versus cloud resolution syntax for conflicts with one cloud candidate.

For multiple cloud candidates, require an explicit cloud candidate ordinal when
resolving, for example `grove conflicts resolve <id> --keep cloud --candidate 2`.
Selecting local continues to use the captured local stage and does not require a
cloud ordinal. A selected historical cloud revision becomes the chosen content
for both the local destination and the Drive head through the existing journaled
resolution path; other preserved revisions remain untouched and pinned until
their conflict-owned stages can be safely cleaned up. The list/show command makes
clear which ordinal is selected.

Resolution holds the established profile and destination exclusions. Before
recording a choice, verify the local candidate and every referenced cloud stage
using identity, size, and hashes. Re-fetch and validate the remote object's
identity, ownership, path, type, and current head against the conflict's recorded
live-head fingerprint. For a selected cloud revision, verify that the revision
identity still refers to the captured candidate and use its verified stage as
the source; never download a different head and label it as the selected
candidate. A stale remote head or changed local candidate refuses resolution
without dropping the pending choice or stages.

`ask` remains the default policy. A configured automatic keep-local or
keep-cloud policy cannot choose among multiple preserved cloud revisions and
must leave the conflict pending for explicit operator selection. The normal sync
path must not reinterpret an unresolved multi-candidate conflict as permission
to overwrite either side.

### Persisted state and interruption recovery

Use a versioned conflict record that can hold multiple cloud candidates while
reading the existing single-cloud-candidate form. Since Grove is pre-release,
compatibility with older deployed versions is not required; current profile data
and pending transactions still require safe, explicit handling. Select a state
versioning strategy in the implementation plan, preserving single-candidate
profiles and refusing malformed or ambiguous records rather than guessing.

Extend `recovery inspect` to report the recorded and verified candidate counts
and an actionable next step when multi-candidate capture is pending. Extend
`recovery retry` to resume only the original upload transaction and finish
capture. It must not start a sync, apply conflict policy, select a candidate, or
write file contents to Drive. `recovery export` continues to support the existing
local/cloud candidate options; export of an arbitrary member of a multi-candidate
conflict is outside this slice unless the implementation design shows it is
needed for safe operator choice.

## Alternatives considered

1. **Keep multiple candidates blocked and export them manually.** This preserves
   current data but cannot safely reconcile the sync profile. It remains the
   current fallback if a candidate cannot be verified.
2. **Expose only the first or newest candidate.** This makes the normal case
   simpler but discards operator choice over other known competing revisions and
   can select a revision based on timestamp ordering that does not prove intent.
3. **Expose every captured revision and require explicit selection.** Recommended.
   It completes the existing recovery model while retaining every candidate and
   keeping the choice with the user.

## Out of scope

Deletion propagation, automatic merging, force-abandon, proving that an
in-flight Drive request has settled, revision pin-quota redesign, cleanup of
unrecorded artifacts, and daemon/UI orchestration remain separate work. This
design does not claim that every concurrent revision can be recovered: Drive may
purge an unpinned revision before Grove can preserve it, and the 200 pinned
revision quota remains.

## Verification expectations

The implementation plan should include deterministic offline tests for:

- one, two, and many candidates with stable ordering and complete anchored
  history;
- interrupted pin/download/state-save boundaries, including restart recovery
  after each candidate and retention of unknown or disappeared obligations;
- corrupt metadata, changed revision bytes, missing stages, identity replacement,
  quota exhaustion, and incomplete revision history;
- listing and displaying candidate descriptions without sensitive values;
- explicit selection of local and each cloud candidate, invalid ordinals,
  changed local or remote state, and preservation of unselected stages;
- a crash during resolution and restart without duplicate uploads or lost
  candidates;
- single-candidate and ordinary two-way conflict behavior remaining intact.

Run formatting, lint, offline and race suites, native macOS build, and Linux
cross-build during implementation. Linux CI provides Linux offline runtime
coverage; live Drive acceptance remains opt-in and requires dedicated test
credentials, disposable local directories, and fixture-owned remote data. Linux
live acceptance should be run on the Linux machine when available. No live test
is authorized by this spec.

The implementation plan must link this spec using
`docs/implementation-plans/000006-multiple-conflict-candidates-plan.md`.
