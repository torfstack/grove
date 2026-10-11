# 0005: Recovery-only operator controls

Date: 2026-10-11. Status: proposed; command scope agreed, detailed spec pending review.

## Proposal

Implement the scope in [spec 000005](../spec/000005-operator-reconciliation-design.md):
offline inspection, an explicit recovery-only retry, and export of verified
transaction stages. The user agreed these three commands after merging PR #7.

Retry must not call the full sync workflow: successful recovery could otherwise
start unrelated uploads. Keep recovery in the shared engine and preserve the
existing profile, destination and authentication exclusion boundaries.

Export copies verified bytes without abandoning the transaction. An unchanged
head or missing allocated create ID cannot establish that an uncertain request
has settled. Force-abandon is deferred; some profiles can remain blocked
indefinitely. Multiple-competitor selection remains a separate design.

## Consequences

This improves diagnosis and access to preserved data, with safe retry of
recoverable transactions. It does not provide a universal unblock/reset command.
Written-spec approval and the paired implementation plan remain outstanding.
