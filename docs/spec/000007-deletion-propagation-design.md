# 000007: Safe local deletion propagation

Date: 2026-10-12. Status: implemented; merged in PR #10 at `8123f12`.

## Intent

Allow an explicit `grove sync --two-way` run on a populated profile to propagate
local deletions by moving the matching Drive items to Drive Trash. A local
deletion is actionable only when the remote object and its tracked subtree still
match Grove's saved baseline. Preserve remote content and report a conflict when
Drive changed. Keep download-only sync free of new remote mutations.

The user selected deletion propagation as the next feature after PR #9 and
approved the design direction: local deletions may trash their Drive counterpart
only after checking that it still matches the saved baseline; remote changes
must be preserved and surfaced for resolution. This first slice covers ordinary
files and folders. Grove remains pre-release; compatibility with earlier releases
is not required, while current profile data and interruption recovery remain
required.

## Approach

Extend the existing deterministic planner and journaled executor. Compare a
complete local scan, complete supported Drive scan, and the durable baseline.
Interpret missing tracked paths as deletion intent only in two-way mode. Order
folder deletions before any descendant deletes by collapsing a missing subtree
to its highest missing tracked folder. A folder is eligible only when the full
remote subtree is complete and contains exactly the tracked identities expected
under that folder; no new, missing, moved, or changed remote descendant may be
silently included in a folder trash operation.

Drive Trash is reversible and must be implemented with the existing trash
boundary, never permanent deletion. Drive reports descendants as trashed through
a trashed parent, so a successful folder-trash operation accounts for its
tracked subtree. The engine commits removal of the corresponding baseline entries
only after it confirms the target folder is explicitly trashed and the tracked
descendants are no longer active. See Google's [Trash or delete files and
folders guide](https://developers.google.com/workspace/drive/api/guides/delete)
and [Drive files resource](https://developers.google.com/workspace/drive/api/reference/rest/v3/files).

## CLI and authorization

- Only `sync --two-way` may start a new trash operation; require the existing
  read-write OAuth scope and an established populated profile.
- A normal download-only sync never starts remote trash operations. If it finds
  an interrupted trash transaction, it may recover that already-journaled
  operation under its existing recovery behavior, but must not plan new deletes.
- Do not add a separate delete command. Use the existing binding flags, locks,
  profile policy, and shared service boundary.
- Report the number of items (files and folders) moved to Trash separately from
  uploaded files, created folders, and downloads. Errors must not expose tokens,
  generated IDs, or remote content.

## Planning and conflict rules

The planner must finish complete local and remote scans before starting any new
trash operation. An incomplete listing, scan error, or malformed state never
authorizes deletion. Match baseline records by stable remote ID, not only by
path. A local deletion is eligible only if its saved path is absent locally and
the remote identity is still at its saved path, parent, and type.

| Local state | Remote state compared with baseline | Two-way result |
| --- | --- | --- |
| Tracked file absent | Same identity, path, type, revision, modified time, size, and MD5 | Journal and trash the Drive file |
| Tracked empty folder absent | Same identity, path, type, and tracked membership | Journal and trash the Drive folder |
| Tracked folder and all descendants absent | Complete subtree exactly matches the saved baseline | Trash the topmost missing Drive folder once; remove its tracked subtree after confirmation |
| Tracked file/folder absent | Remote identity, path, type, or relevant baseline fingerprint changed | Preserve Drive item and create a durable deletion conflict |
| Missing tracked folder | Remote subtree has an untracked/new descendant, missing identity, move, or changed member | Do not trash the folder; preserve the subtree and create a durable deletion conflict |
| Tracked item absent remotely | Remote listing is complete and ID is absent | Preserve local data and report the existing unsupported remote-removal condition; do not infer that local deletion was completed |
| Tracked item absent remotely | Listing is incomplete or item cannot be conclusively located | Fail closed and preserve local data |
| Tracked item is locally present but changed | Any | Keep existing upload/conflict behavior; deletion is not inferred |

The remote fingerprint for binary files includes identity, parent, name/path,
type, head revision, modified time, size, and MD5. Metadata-version-only increases
may be accepted only under the existing upload rule when all content and path
fields still match. Folder comparisons use identity, parent, name/path, type,
metadata version and complete tracked descendant membership; folders have no
binary revision checksum. Any uncertainty leaves the remote item untouched.

Deletion conflicts are distinct from content-edit conflicts: their local
candidate is an explicit tombstone, not missing staged bytes. Extend persisted
conflict records and `conflicts show/resolve` to display that fact. `ask` keeps
the conflict pending. Do not apply `keep-local` or `keep-cloud` automatically to
a deletion conflict. An explicit `--keep local` resolution may trash the
conflicted remote item (or the validated remote subtree) after rechecking the
recorded live state; `--keep cloud` restores the remote file/folder tree locally
through verified normal download operations. If revalidation fails, retain the
conflict and all content. Existing content conflicts and multi-candidate ordinal
semantics remain unchanged.

Each deletion conflict stores both the old tracked baseline subtree and a
fingerprint of the live remote subtree shown for resolution. Resolution requires
the live identities, paths, and relevant metadata to match that saved fingerprint.
The local choice trashes the reviewed live subtree while removing only tracked
baseline entries; the cloud choice drops the selected tombstone baseline before
the normal planner restores all current remote descendants. A cloud restore marks
the profile incomplete until every restore operation finishes, preventing a
partially restored child from being interpreted as a new local deletion. A
download-only sync safely resumes an interrupted restore. The `Trashed` result
counts each remote file and folder, including descendants moved with a parent.
Other pending conflicts remain inspectable and explicitly resolvable while that
restore is incomplete. Restoration planning preserves their local candidates
and tombstones and never infers a new remote deletion.

Do not infer local renames from missing paths, hashes, or same-name entries.
Local moves, arbitrary path rearrangements, deletion exclusions, Google-native
documents, shortcuts, shared drives, and deletion propagation for preexisting
untracked local content remain out of scope.

## Transaction and recovery

Add a typed trash operation to the existing sync journal. Before the Drive
mutation, persist the remote identity, prior baseline record(s), expected
structure, and transaction phase. Re-fetch the item immediately before trashing
and validate ownership, active status, identity, parent/path, type, and the
fingerprint above. For a folder, record the complete tracked subtree identities
and fingerprints and ensure the remote listing is still complete before mutation.

After a successful request, independently fetch the folder/file by ID and verify
it is explicitly trashed. For a folder, verify its tracked descendants report
trashed through the parent; do not individually trash the descendants. The Drive
adapter must request the explicit-trash metadata needed to distinguish the
folder itself from an item only trashed through an ancestor. Only then atomically
remove the affected baseline entries while retaining cleanup intent until
journal retirement is durable. If the request outcome is uncertain, recovery
fetches the same recorded identity: confirmed trash completes the baseline
change; an active, changed, missing, or unverifiable item keeps the transaction
blocked. Recovery never repeats a remote request whose outcome is uncertain,
allocates a new identity, or selects a replacement object. A failure known to
occur before the request was sent may safely retire the untouched intent.

Confirmation also rechecks recorded parent/name/type, ownership, content revision,
modification time, size, and MD5. Metadata version increases are expected for the
Trash mutation and need not equal the preflight version afterward. A changed
fingerprint or moved descendant retains recovery evidence instead of removing
its baseline.

The Drive API does not offer a conditional trash operation. A concurrent remote
change between Grove's final preflight read and the trash request is therefore a
residual race. Keep the time between read and mutation minimal, verify afterward,
and retain recovery evidence on any mismatch. This does not weaken the required
preflight rule: Grove never intentionally trashes an item it observed as changed.

The profile lock and existing destination/token exclusion cover planning,
trash, verification, and commit. Concurrent writers for a profile remain
refused. A process crash at every persisted phase must either safely finish the
same operation or retain enough evidence to stop without losing local state.

## Error behavior and limits

- Unsupported access, ownership, incomplete subtree membership, or changed
  preconditions stop before the relevant trash mutation.
- If an earlier independent operation completed before a later error, retain the
  existing per-operation durability guarantee; the whole Drive tree is not an
  atomic transaction.
- Drive may trash descendants recursively with a folder. Therefore folder trash
  is allowed only after validating every currently listed descendant against
  the saved subtree. Permission failures or ambiguous child state preserve the
  folder and create/retain a conflict rather than attempting partial cleanup.
- A folder restored from Trash may restore its descendants according to Drive's
  own behavior. Grove does not promise to restore external sharing, revision
  history, or items changed while the conflict was pending.
- The feature does not empty Trash or permanently delete Drive content.

## Verification and acceptance

Deterministic planner and fake-Drive integration tests must cover:

- two-way opt-in versus read-only mode and no new remote mutations in ordinary
  sync;
- deletion of one file, empty folder, and nested populated folder, with one
  topmost folder trash operation and correct subtree baseline removal;
- changed file revision/content/path/type, changed folder metadata, new remote
  descendants, missing descendants, and incomplete listings, all preserving
  remote content;
- deletion conflicts in ask mode, explicit local trash after revalidation,
  cloud-side restoration, and refusal after conflict state changes;
- trash request failures, uncertain outcomes, state-save failures, process
  interruption before/after remote success, retry, and idempotent no-op repeats;
- local edits, local renames, and unsupported filesystem entries continuing to
  fail without accidental deletion;
- sanitized CLI output and accurate trash counts.

Extend the fixture-owned opt-in live scenario to trash a file and a nested folder
in the disposable test account, verify both appear in Drive Trash and their
descendants are inactive, verify an unchanged rerun is a no-op, then clean up
only fixture-owned objects. Live tests require explicit enablement and dedicated
credentials. Linux live runtime acceptance should be run on the Linux machine;
cross-build alone is not runtime acceptance.

The implementation should run `mise run fmt`, `mise run lint`, `mise run test`,
`mise run test-race`, native build, and Linux amd64 build. Completion requires
offline and race verification plus live or explicitly documented live-test
limitations, and updates to status, product scope, architecture, roadmap, and a
decision record.

## Implementation plan

The paired [implementation plan](../implementation-plans/000007-deletion-propagation-plan.md)
is approved for native execution on `feat/deletion-propagation`.
