# Local uploads with preserved conflicts

Date: 2026-10-10. Status: proposed design for user review; implementation is not approved.

## Intent and scope

Make an established Grove profile useful for local editing: upload new ordinary
files and folders, and local content edits, while retaining incremental downloads
in the same shared engine. The user requested this spec after selecting uploads
as the next design. The detailed behavior below is proposed, not yet agreed.

Success means independently verified remote bytes, a durable baseline, no duplicate
creates after interruption, and preservation of conflicting content. Linux remains
the primary acceptance target. Deletion propagation, local rename detection,
automatic conflict resolution, daemon orchestration, native documents, shortcuts,
shared drives, exclusions, and arbitrary path rearrangements remain outside scope.

## Approach and alternatives

Recommend explicit two-way opt-in on an already populated profile. Reuse complete
scans, stable Drive identities, content hashes, the deterministic planner, and the
sequential journaled executor. Retain download-only behavior by default.

An additions-only slice avoids overwriting existing remote files and is the fallback
if conditional updates cannot be proven safe. It would not support normal local
editing. A separate upload command or engine would duplicate reconciliation and
recovery and make mixed remote/local changes harder to reason about. Automatic
winner selection would expand scope and risk losing edits.

## Command, authorization, and compatibility

Propose `grove sync --two-way` using the existing binding flags. Without this flag,
local changes retain today's strict preflight errors. A normal download-only run
must never issue a new remote mutation. Require completed initial population before
two-way mode: absent/empty first destinations still follow spec 000002. Uploading
an arbitrary preexisting local tree as initial population is deferred.

The opt-in is per invocation; do not silently enable future remote writes. Require
an explicitly read-write token before executing or recovering a remote write.
Never expand OAuth scopes automatically. Preflight validates file update and folder
child-creation capabilities as appropriate; scope alone does not establish access.
All existing token, profile, destination, and ancestor locks continue to apply.

A pending upload must be reported when invoked without `--two-way`, with a request
to rerun in that mode; it cannot silently fall through to download recovery. Old
download transactions retain their existing recovery behavior. Output adds uploaded
file and remote-folder counts and distinguishes conflicts from uncertain outcomes.
Errors omit credentials, generated IDs, upload session URLs, and file contents.

## Reconciliation rules

Compare a complete local scan and complete supported remote scan with the durable
baseline. Local timestamps cannot establish unchanged content. A failed or incomplete
scan starts no new local or remote mutations. Preflight rejects all known conflicts
before executing independent new operations under the `ask` policy; earlier
completed operations and already journaled recovery retain the existing
nontransactional-tree guarantee.

Persist a profile-scoped conflict policy with three values: `ask` (default),
`keep-local`, and `keep-cloud`. A policy applies only to simultaneous content
changes for an existing tracked file. It does not authorize resolving missing
files, renames, type changes, path collisions, unsupported content, or uncertain
remote outcomes. `ask` stores a pending conflict without mutation. `keep-local`
uploads the local snapshot through the guarded update path; `keep-cloud` downloads
the remote snapshot through the existing verified replacement path. If an
automatic choice cannot pass its required preconditions, preserve both versions
and leave an actionable conflict rather than falling through to an overwrite.

Conflict decisions are made by the shared engine, regardless of whether a run
starts from the CLI or daemon. The policy lives in the private profile record,
defaults to `ask` when absent during migration, and is changed only through a
profile-locked service operation. Do not duplicate policy parsing or resolution
logic in a UI or command handler.

| Local versus baseline | Remote versus baseline | Proposed action |
| --- | --- | --- |
| Unchanged | Unchanged | Skip; no media transfer or destination write |
| Unchanged | Content changed | Existing verified download replacement |
| Content changed | Unchanged identity, path, version and content | Conditional upload update |
| Content changed | Any changed remote version, content or path | Apply profile policy; `ask` preserves both and waits |
| Unchanged | Metadata only changed | Existing record operation |
| Unchanged | Renamed/moved | Existing safe move behavior |
| Missing tracked path | Any | Unsupported local removal/rename; preserve remote |
| Any tracked entry | Missing remote ID | Unsupported remote removal; preserve local |
| New local path | No remote entry at that path | Create, after parent/collision validation |
| No local entry | New remote path | Existing download/create behavior |
| New local path | New remote entry at same path | Conflict, even if bytes match |

Treat simultaneous edits as conflicts even if the resulting bytes match; policy,
not byte equality, chooses whether to ask or select a side. A remote same-byte
version change alongside a local edit is also a conservative conflict. Missing tracked directories,
type changes, duplicate names, symlinks, special files, unsupported names, filesystem
aliases, occupied targets, swaps, and cycles retain strict rejection.

Support new local folders, including empty folders and nested ordinary files.
Create parents before children and persist each confirmed parent identity before
child operations. Do not infer local moves from hashes. New local descendants
inside a remotely moved folder are a structural conflict for this slice; reject
before executing that move. Do not upload into a path vacated by another operation
in the same run. Independent local additions and remote additions at distinct safe
paths may share a plan.

## Remote write safety and feasibility gate

An update must be guarded by a server-enforced precondition tied to the exact
remote representation validated against the baseline. A GET immediately followed
by an unconditional PATCH is insufficient: another client can edit between them.
Drive's monotonic `version` is an observation, not an assumed write precondition.
The current fixture Update adapter provides no conditional-write guarantee and
must not be reused unchanged for production sync updates.

Before approving an implementation plan for existing-file updates, establish and
record the supported Drive v3 conditional media-update mechanism, including its
behavior across resumable session initiation and final publication. Require an
explicitly enabled disposable-account experiment showing that a stale condition
rejects publication and preserves the competing edit. Fake-HTTP tests alone cannot
prove Google's enforcement. Do not assume an ETag field exists in the v3 JSON
resource or invent a `version` query parameter.

If the mechanism is unavailable or cannot be demonstrated, revise this spec to
additions-only and obtain approval for that narrower slice. Do not weaken the
preservation requirement to a check-before/check-after overwrite. This is the
remaining technical design question; no production update implementation should
start while it is unresolved.

For creation, allocate a Drive-generated ID and durably associate it with the
operation before issuing the create. Use the same ID on reconciliation/retry,
plus a random private operation marker in appProperties. Identity allocation
without a subsequent durable intent may leave an unused ID, but cannot create an
untracked remote object. Ordinary binary files and folders are the supported types.
ID allocation prevents duplicate-ID creates; Drive duplicate sibling names still
require fresh parent/name checks. Drive does not promise atomic name uniqueness.
A concurrently introduced same-name object stops reconciliation and leaves both
objects intact; no remote deletion or automatic adoption is authorized.

## Upload execution and recovery

Keep local files in place throughout uploading. Stream a root-confined local file
into a private, exclusively created staging file; record artifact ownership and
verify SHA-256, MD5, and size against the planned bytes before remote mutation.
This gives retries immutable bytes without loading an entire user file into memory.
Recheck the local path's content and relevant parent membership before sending;
detecting a change aborts new work while preserving the local file.

Use a streaming resumable upload boundary for ordinary files, including an explicit
zero-byte path. Bound memory and retry budgets. Session URLs are sensitive private
journal data. After session loss, reconcile remote identity and outcome before
starting another request; never blindly replay an uncertain update.

The expanded versioned journal records operation kind, source path and hashes,
old baseline and remote precondition for updates, allocated ID for creates, expected
parent/name/type, operation marker, owned stage, session state, and verified result.
Pending user decisions are profile state; once selected, the choice and expected
candidate identities are copied into the operation journal before mutation.
Persist intent before every externally visible write. The plan must define exact
phases and invariants, not add unrelated flags to the existing download transaction.

| Interruption/outcome | Required restart behavior |
| --- | --- |
| Stage exists without durable ownership | Preserve and stop for inspection, matching current safety policy |
| Intent durable, no request issued | Revalidate local/remote preconditions before sending |
| Session active or response lost | Query session/recorded remote ID; retain uncertain intent on errors |
| Create ID exists | Verify operation marker, parent, name, type and content; never adopt an unrelated object |
| Create returns duplicate-ID conflict | Reconcile that ID; do not allocate another ID |
| Update condition rejected | Preserve local stage and remote object; report conflict, no unconditional retry |
| Remote result differs from intended bytes or structure | Preserve intent and local data; stop without compensating mutation |
| Remote success confirmed, baseline save fails | Retain intent; reconcile and finish recording without uploading again |
| Baseline durable, cleanup interrupted | Remove only identity/hash-verified stage, then clear intent |

Confirm remote file size and MD5, then independently read remote content to verify
the staged SHA-256, with stable metadata/version checks around verification. Folder
creates verify identity, marker and parent/name/type. Record the verified remote
version and uploaded hash as the baseline, never merely the current local bytes.
If the user edits locally during an upload, record only the uploaded snapshot;
stop the run with a local-change report and preserve the newer local bytes for the
next invocation. Do not upload them automatically in the same run.

An uncertain update is complete only when the recorded operation and observed
remote result can be reconciled unambiguously; matching checksum alone is not
proof of who wrote it. If remote state advanced after an uncertain request, preserve
intent and report reconciliation required. No automatic rollback, trash, deletion,
or overwrite resolves ambiguity. Retained stage data remains private until safe
cleanup or a separately designed operator reconciliation command.

## Architecture and state migration

Extend the existing engine rather than adding a second synchronization workflow.
The planner remains pure; scanners classify local/remote/baseline changes, Drive
owns ID allocation and guarded streaming writes, the executor owns staging and
journal transitions, and the service owns locks and resource lifecycle. Keep
fixture mutation separate from the production upload contract while sharing
proven HTTP primitives where their requirements coincide.

Make these boundaries explicit in the code:

- `internal/syncengine` owns the `ConflictPolicy` type, conflict classification,
  durable pending-conflict records, policy application, and revalidation before
  a chosen action. Planner output identifies conflicts and possible choices; the
  executor performs the selected operation through the existing journal rules.
- Profile state stores the policy and pending conflict identities alongside the
  sync baseline and transaction. A missing policy in older valid state means
  `ask`. State is read or changed only while holding the existing profile lock;
  callers do not edit JSON directly.
- `internal/drive` remains a transport boundary for guarded uploads and downloads;
  it does not decide policy or present prompts.
- `internal/cli` exposes profile policy configuration and conflict inspection and
  resolution through injected engine/service methods. Proposed command shapes are
  `grove config set conflict-policy ask|keep-local|keep-cloud`,
  `grove conflicts list`, and `grove conflicts resolve <id> --keep local|cloud`.
  Final command syntax is subject to CLI review in the implementation plan.
- `groved` calls the same service methods and publishes policy and pending
  conflict state to its future UI-facing interface. With `ask`, it pauses the
  affected work and waits for a CLI or UI resolution; it never blocks on a terminal
  prompt. The UI is a client of this boundary, not a second policy implementation.

Conflict records carry a stable conflict ID, local relative path and snapshot
hash/size, remote ID and observed version/content identity, and the baseline used
to classify the conflict. Resolution records the selected side durably before
execution. Before applying it, the engine rescans/rechecks local bytes, remote
identity and version, and destination preconditions. If either candidate changed
since the decision, leave the conflict pending and request a fresh choice. Resolving
one record does not silently resolve other conflicts in the profile.

The first implementation may provide the shared service boundary and CLI controls
without a graphical UI. `groved` must still consume the profile policy and expose
pending conflicts through a stable machine-readable status/resolution API so a
later UI can use the same behavior. Do not require simultaneous CLI and daemon
execution; existing profile locking serializes writers.

Propose version 3 for upload intents and any remote representation validators.
Recover valid version-1/version-2 download work with existing semantics before
atomic migration under lock. Preserve bindings and registrations. Older binaries
must reject version 3. If historical baselines lack a required validator, establish
one only after current remote identity/version/content/path still match the saved
baseline; never establish a new baseline by accepting a changed remote or local
file. Reject malformed or unsupported records before mutation.

Refactor only touched scanning, comparison, transfer, and journal boundaries.
Avoid generic transaction frameworks, parallel uploads, speculative optimization,
and unrelated package moves. Single-profile exclusion does not lock other Drive
clients or local editors; the design relies on guarded writes and preservation.

## Verification and completion

Use deterministic planner tests for every table row and each conflict policy,
pending-conflict creation, stale resolution rejection, and policy migration,
nested/empty additions,
remote/local structural interactions, capability failures, unsupported entries,
and unchanged repeats. Assert no new writes on failed preflight and no remote
writes without explicit opt-in, including restart with pending upload work.

Test real staging ownership and changes during staging/upload. Inject failure at
intent, ID/session persistence, send, ambiguous response, remote verification,
baseline save and cleanup boundaries. Exercise expired sessions, duplicate-ID
responses, competing remote writes, cancellation, zero-byte and streamed large
files, malformed journals, old-state recovery/migration, and writer exclusion.

Extend the independent HTTP fixture workflow: populate → local additions/edits →
two-way sync → independently verify remote and local bytes → no-op → conflicting
edits on both sides → assert preservation → fixture-owned cleanup. Reuse fixture
ownership for live data. Live tests require explicit enablement, dedicated profile
and account, disposable directories, and fixture-owned remote objects. Do not
print tokens, IDs, session URLs or synced contents. No live test is authorized by
writing this spec.

Implementation requires `mise run fmt`, `mise run lint`, `mise run test`,
`mise run test-race`, native build and Linux build. Report Linux runtime/live
acceptance separately from macOS acceptance and cross-compilation. Complete only
after demonstrated behavior, conditional-write evidence if updates are included,
independent verification, and updated product, architecture, testbed and status.

## Sources and next artifact

- [Drive upload guide](https://developers.google.com/workspace/drive/api/guides/manage-uploads): resumable uploads and pre-generated IDs for retryable creates.
- [Create/manage files](https://developers.google.com/workspace/drive/api/guides/create-file): supported pre-generated-ID file types.
- [files.update reference](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/update): media and metadata update interface.
- [v2/v3 comparison](https://developers.google.com/workspace/drive/api/guides/v2-to-v3-reference): removal of the JSON File `etag` field in v3.

These references do not by themselves establish a conditional media-write
guarantee. The feasibility gate above remains explicit rather than claiming one.

After user review and resolution of that gate, the paired implementation plan is
`docs/implementation-plans/000004-local-upload-plan.md`. It must link to this spec
and specify the concrete API contract, journal phases, crash matrix, migration,
and acceptance workflow before product implementation.
