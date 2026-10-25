# 0012: Opt-in remote change hints

Date: 2026-10-25. Status: direction authorized by the user; detailed choices
made by the agent under standing autonomy; implementation and acceptance complete.

The user authorized remote change detection after dedicated-account live watcher
acceptance. [Spec 000012](../spec/000012-remote-change-detection-design.md)
records the contract; individual settings below are delegated agent choices.

Use optional remote_poll_interval (minimum 10s, examples 30s) to poll Drive's
My Drive changes feed. Keep complete periodic scans and the same shared engine's
binding, locking, conflict, revision and deletion semantics. Feed entries carry
no operation authority; outside-root changes may cause harmless extra scans.
This is preferred to frequent full-tree scans or a public webhook service.

Keep cursors in memory. Initialization emits a dirty hint so a normal scan follows
cursor capture; restarts obtain a new cursor and reconcile normally. Advance only
after complete bounded pagination, retaining original cursors and partial dirty
evidence on failure. This avoids a persisted-state/migration protocol at the cost
of an initialization scan and operator restart for repeatedly rejected cursors.

Serialize bounded read-only checks and RunExisting through cleanup, using one
context-aware gate. The ten-second check budget includes waiting, and a sync may
wait for an already active check. This preserves same-daemon token/profile
exclusion rather than creating self-busy attempts. Ordinary check failures retry
at the remote interval; genuine session/lease cleanup failures stop visibly and
retain cancellation causes. Historical status remains distinct from liveness.

Verification includes deterministic gate/source/coordinator tests, fake HTTP
pagination, locked-service tests and real daemon processes. Remote-only empty
feed checks retain prior sync counts/history; they do not invent an unrequested
no-op engine attempt. Combined live acceptance requests explicit local metadata
hints for later no-op cycles. No tokens, cursors, generated IDs or synced contents
are recorded in public evidence.

Review clarified that fatal remote cleanup cancels the shared lifetime before
releasing the gate. This prevents queued engine work from starting against an
uncertain cleanup state; remote-only and combined regressions enforce the order.
