# 0004: Explicit uploads with guarded remote updates

Date: 2026-10-10. Status: proposal; detailed design awaits user review.

## Direction and proposed decision

The user requested a local-upload spec after completion of incremental downloads.
[Spec 000004](../spec/000004-local-upload-design.md) proposes extending the shared
engine with per-invocation two-way opt-in on established profiles. Profile-scoped
conflict policy (`ask`, `keep-local`, `keep-cloud`) and pending records are owned
by the shared engine and consumed through service methods by both CLI and daemon.
Journal immutable upload snapshots and reconcile uncertain creates through durable
pre-generated IDs. Deletions and local moves are deferred.

Existing-file updates must have demonstrated server-enforced preconditions;
preflight metadata checks alone cannot protect competing remote edits. The current
fixture Update method provides no such guard. Drive v3 conditional media-update
behavior remains a feasibility question, requiring documented evidence and an
explicitly enabled disposable-account experiment before an update implementation
plan is approved. If the guarantee cannot be established, propose additions-only
and obtain approval for revised scope.

## Live feasibility result and narrowed-scope proposal

On 2026-10-10, the explicitly enabled live suite passed its initial and
incremental download scenarios. A separate fixture-owned resumable-update
experiment found no HTTP ETag on the Drive v3 metadata response. It then used
the Drive `version` value as an `If-Match` candidate: the matching-version control
was accepted, and a later request using the now-stale version was also accepted
with HTTP 200. Independent download confirmed that it had replaced the competing
bytes. The experiment restored those bytes and cleaned up only its owned fixture.
This does not establish a usable server-enforced conditional update.

Propose revising spec 000004 to an additions-only first slice:

- Two-way mode may create new local files and folders on established profiles,
  using preallocated Drive IDs, durable intent, and fixture-verified recovery.
- It must not update existing remote file content. Tracked local edits, path
  collisions, removals, and unsupported structural changes remain non-mutating
  errors that preserve candidates.
- Defer profile conflict-policy configuration and conflict list/resolve commands
  until a safe existing-file update mechanism exists; the already committed
  profile-state types remain groundwork and are not exposed as active settings.
- Keep download-only behavior as the default and require explicit two-way
  authorization for every create.

This revision is proposed, not agreed. Do not continue implementation past the
completed profile-state foundation until the user approves the revised scope and
paired plan.

## Consequences and review

Download-only behavior remains the default. The additions-only proposal adds
private staging, a new journal version, remote-outcome reconciliation, and
write-capability validation. No upload production behavior is implemented yet.
Approval of the revised spec and paired implementation plan remains required.
