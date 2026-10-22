# Download consistency across metadata-only Drive changes

Date: 2026-10-22. Status: draft for user review; implementation not started.

## Intent and evidence

Make initial and incremental downloads tolerate a Drive metadata-version increase
when Grove can prove the file's content revision and structure still match the
planned download. Preserve detection of content changes, moves, deletion,
unsupported metadata, and local conflicts, including after interruption.

The user selected this reliability ticket after Linux live acceptance and asked
to start with a written spec and a dedicated branch. Work is on
`fix/download-metadata-version`. This draft proposes the details below; they are
not yet agreed implementation requirements.

At `8123f12`, the Linux live suite failed initial sync because the post-download
metadata version differed from the scan. Identity, parents, size/MD5, ownership,
and active status matched. A fresh initial-sync retry passed. The same symptom
was recorded on macOS. The existing error does not report head-revision or
modified-time comparisons, so these observations alone do not prove that the
failure qualifies for the proposed exception.

Google documents `version` as advancing for server changes, including changes
not visible to the user, and `headRevisionId` as the binary file's current
revision. See the official [File resource reference](https://developers.google.com/workspace/drive/api/reference/rest/v3/files).
The specific cause of the observed version advance remains unknown. This fix
must not depend on attributing it to seeding, inspection, or another operation.

## Scope and alternatives

Use the existing shared sync engine, complete scans, exclusive staging files,
checksum verification, and durable journals. Change download validation in
normal execution and recovery, including new files, replacements, and downloads
used by cloud restoration. Keep the change local to these paths.

Three approaches were considered:

- **Recommended: validate content revision and structure.** Allow a higher
  metadata version only with the stronger evidence defined below. Apply the
  same rule during transfer and recovery.
- **Keep strict versions and retry or wait.** This retains false failures,
  adds timing assumptions, and cannot guarantee eventual completion. A delay
  or automatic test retry must not be the fix.
- **Download a specific revision.** This would change download semantics and
  add revision-access and retention concerns. It is unnecessary for this slice,
  which must still reject an observed change of the current head.

No new CLI flags, remote writes, revision pins, state formats, daemon work,
global retry policy, or generalized fingerprint framework are proposed. Move,
upload, and Trash preconditions retain their existing rules. This proposal
supersedes strict version equality only at the download boundaries described
here; it does not rewrite the historical requirements in specs 000002/000003.

## Proposed validation rule

Compare the planned file metadata with the fresh observation after transfer or
at a download recovery boundary. Both must describe a supported ordinary binary
file. Always require:

- the same nonempty remote ID, name, and exact MIME type;
- exactly one matching parent, account ownership, no shared-drive ID, neither
  inherited nor explicit Trash, and download capability;
- present nonnegative size and valid MD5, equal in both observations;
- nonempty version metadata;
- matching head revision and modified time whenever either observation contains
  those fields; a field disappearing or appearing is not treated as equality.

Version equality remains sufficient for the version part of the check. If the
versions differ, accept only when both are valid positive decimal int64 values,
the fresh version is numerically greater, and both observations also contain
the same nonempty head revision and valid, exactly matching RFC 3339 modified
time. Never infer a content revision from size/MD5 alone. A changed version with
missing revision/time evidence, a lower version, or malformed version metadata
fails closed. Files without revision/time evidence can still pass the strict
equal-version path; this is not permission to accept their version changes.

Independently count and hash the actual downloaded bytes: size and MD5 must
match the plan, and compute local SHA-256 for journal and baseline verification.
A matching metadata fingerprint cannot override a checksum mismatch, read
error, cancellation, or failed persistence. Do not publish unverified bytes.

For initial-download recovery, also require the current scan's relative path
to match the pending path; this catches an ancestor move even if the immediate
parent ID is unchanged. Directory recovery keeps its existing behavior and
does not gain a binary-file version exception.

This comparison proves equality at the observed boundaries, not that Drive was
unchanged between them. A later remote edit remains detectable by subsequent
sync; the operation does not promise a transactionally consistent remote tree.

## Execution, persistence, and recovery

Use a narrow comparison shared by post-transfer verification, pending initial
file recovery, and the remote check for an unpublished replacement. Do not
relax the general `currentRemote` check used by move and Trash operations.
Prefer small functions in the existing engine over a second validation layer.

Retain the planned remote snapshot in pending records, replacement operations,
their verified results, and the completed baseline. A successful version-only
comparison proves those planned bytes remain the current content; it does not
rewrite the operation to a new remote identity. A subsequent complete scan can
advance the saved version through the existing metadata-record operation,
without media transfer or destination-file changes. This keeps journal
validation and before/after records consistent without adding a state field.

Recovery must tolerate further qualifying version advances relative to that
same saved snapshot. In particular:

- An interrupted unverified initial download may remove only its confirmed
  owned temporary artifact and retry through ordinary planning.
- Adoption of an already published initial file still requires the durable
  verified phase, its saved SHA-256, size/type checks, and matching current
  remote content and structure.
- An unpublished replacement still verifies its stage and old local target,
  then uses the proposed remote rule before moving/publishing. Version-only
  changes must not cancel the replacement or leave it permanently blocked.
- A real remote change retains the existing safe cancellation or preservation
  behavior for that phase. Published or committed replacements retain their
  existing recovery semantics; this work adds no new rollback policy.
- Save failures and restarts must preserve enough intent and artifact ownership
  evidence to finish safely or stop. No blind cleanup, destination overwrite,
  or journal deletion is introduced.

Grove is pre-release. Earlier-release migration support is unnecessary, and
this proposal expects no schema change. Existing journals lacking revision/time
evidence remain subject to the strict version check; do not fabricate evidence
or reset a blocked profile to make it pass.

## Diagnostics

On a failed comparison, report the boundary and which categories failed:
identity/type, parent/path, version validity/order, content revision, modified
time, size/checksum, ownership/location, Trash, or download capability. Report
comparison results without raw remote IDs, revision IDs, versions, timestamps,
credentials, or file contents. Preserve underlying errors through existing
sanitized error boundaries. This should make a future live failure distinguish
a qualifying version-only advance from an actual revision or structural change.

## Verification and completion criteria

Deterministic fake-provider and engine tests must first reproduce the rejected
version-only increase, then demonstrate:

1. Initial download and incremental replacement succeed when version increases
   but complete revision, time, content, and structural evidence match. Include
   zero-byte files and a numeric version boundary such as 9 to 10.
2. Independent changes to revision, modified time, bytes/MD5/size, name, parent,
   ID, MIME type, ownership, shared-drive membership, Trash flags, or capability
   are rejected. A same-size/same-MD5 revision change must still be rejected.
3. Missing evidence, malformed or regressing changed versions, API failure,
   cancellation, and checksum failure do not publish a new target or destroy a
   previous local target. Equal-version cases retain their documented fallback.
4. Initial recovery in intent/downloading/verified phases handles qualifying
   version advances. Published-file adoption verifies SHA-256; an ancestor move
   or changed local artifact is rejected.
5. Replacement recovery before publication tolerates qualifying advances and
   still protects the old target and backup. Save-fault/reload coverage around
   verification, publication, and baseline commit proves journal consistency.
6. Completed baselines can reconcile the newer metadata through a subsequent
   scan without another media transfer or changed destination-file mtimes.
   Download-based cloud restoration uses the same validation rule.
7. Existing move, upload, Trash, profile locking, incomplete-listing protection,
   and local-conflict tests remain valid. Diagnostics expose no raw metadata.

For implementation, run `mise run fmt`, `mise run lint`, `mise run test`,
`mise run test-race`, and the native build. Run the explicitly enabled full
Linux live suite with the dedicated token, disposable local state, and owned
fixtures. It must pass initial/incremental sync, pinning, stale-write probing,
upload recovery, and deletion, including independent verification, no-op repeats,
and owned cleanup. A focused retry cannot turn a failed aggregate run into a
passing acceptance result. Repeated clean runs are supporting evidence, not a
substitute for deterministic regression coverage.

Update status, testbed results, product behavior, and architecture descriptions
with the implemented rule and actual verification outcomes. After this draft is
approved, write the paired plan at
`docs/implementation-plans/000008-download-metadata-version-plan.md`.
