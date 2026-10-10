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
plan is approved. If the guarantee cannot be established, evaluate revision-based
recovery before falling back to additions-only.

## Live feasibility results and recovery proposal

On 2026-10-10, the explicitly enabled live suite passed its initial and
incremental download scenarios. A separate fixture-owned resumable-update
experiment found no HTTP ETag on the Drive v3 metadata response. It then used
the Drive `version` value as an `If-Match` candidate: the matching-version control
was accepted, and a later request using the now-stale version was also accepted
with HTTP 200. Independent download confirmed that it had replaced the competing
bytes. The experiment restored those bytes and cleaned up only its owned fixture.
This does not establish a usable server-enforced conditional update.

A separate fixture-owned live spike then simulated the same stale-write sequence
and found the competing bytes as a binary revision after Drive accepted the
overwrite. The test marked that revision `keepForever`, independently downloaded
it, verified the competing bytes, and cleaned up the fixture. This suggests a
possible recovery-based design: allow the head write, then immediately pin and
retain any competing revision, record a pending conflict, and require a fresh
user choice before continuing. This is not equivalent to preventing the stale
write; the Drive head temporarily contains the local candidate. The documented
revision quota and purge rules also need to be part of the safety analysis.

Propose evaluating this revision-aware conflict recovery before choosing the
additions-only fallback. Keep the profile conflict policy and CLI/daemon service
boundary in scope if the model can preserve both candidates reliably. This design
is proposed, not agreed. Do not resume product implementation until the user
reviews the head-replacement and revision-retention trade-off and the paired plan
is updated.

## Consequences and review

Download-only behavior remains the default. Either upload path adds private
staging, a new journal version, remote-outcome reconciliation, and write-capability
validation. No upload production behavior is implemented yet. Approval of the
revised safety model and paired implementation plan remains required.
