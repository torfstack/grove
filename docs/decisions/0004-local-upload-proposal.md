# 0004: Explicit uploads with revision-aware conflict recovery

Date: 2026-10-10. Status: accepted.

## Decision

The user requested a local-upload spec after completion of incremental downloads.
[Spec 000004](../spec/000004-local-upload-design.md) proposes extending the shared
engine with per-invocation two-way opt-in on established profiles. Profile-scoped
conflict policy (`ask`, `keep-local`, `keep-cloud`) and pending records are owned
by the shared engine and consumed through service methods by both CLI and daemon.
Journal immutable upload snapshots and reconcile uncertain creates through durable
pre-generated IDs. Deletions and local moves are deferred.

Prioritize local uploads when the current remote still matches the saved baseline.
Before an existing-file update, re-fetch metadata, compare it with the baseline,
and pin the current binary head revision. Abort before the write if the remote has
already changed, the head revision is unavailable, or pinning fails. Drive does
not enforce conditional media updates, so a concurrent write after preflight may
become a non-head revision during Grove's upload. Grove immediately pins,
downloads, and verifies divergent revisions and records a pending conflict. The
Drive head can temporarily contain the local upload while the conflict is
pending. If the competing revision cannot be captured, retain the local stage,
pinned baseline, and journal and stop further profile writes.

This model optimizes the expected common case and accepts the narrow risk that
Drive may purge a competing revision before Grove pins it. The known preflight
revision is protected before mutation; Drive permits at most 200 pinned revisions
per file. When the quota is full, fail closed before uploading.

## Live feasibility evidence

On 2026-10-10, the explicitly enabled live suite passed its initial and
incremental download scenarios. A separate fixture-owned resumable-update
experiment found no HTTP ETag on the Drive v3 metadata response. It then used
the Drive `version` value as an `If-Match` candidate: the matching-version control
was accepted, and a later request using the now-stale version was also accepted
with HTTP 200. Independent download confirmed that it had replaced the competing
bytes. The experiment restored those bytes and cleaned up only its owned fixture.
This does not establish a usable server-enforced conditional update.

A separate fixture-owned live spike simulated the stale-write sequence and found
the competing bytes as a binary revision after Drive accepted the overwrite. It
marked that revision keepForever, independently downloaded it, verified the
competing bytes, and cleaned up the fixture. A subsequent focused live test
verified that the current head can be pinned before an update and unpinned after.
The user accepted revision-aware recovery, prioritizing normal uploads when the
preflight remote matches the baseline.

## Consequences and review

Download-only behavior remains the default. The upload implementation adds
private staging, a new journal version, remote-outcome reconciliation,
revision preservation, and write-capability validation. Profile policy and
pending-conflict service operations remain shared-engine behavior for a future
daemon/UI client.
