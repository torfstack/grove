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

## Consequences and review

Download-only behavior remains the default. Uploads add private staging, a new
journal version, remote-outcome reconciliation, and write-capability validation.
No new production behavior is implemented by this proposal. Spec approval,
feasibility resolution, and approval of the paired implementation plan remain
before implementation.
