# 0009: One CodeRabbit review per pull request

Date: 2026-10-24. Status: accepted by the user.

Use one completed CodeRabbit review per PR. Read its summary, inline comments,
and threads, assess findings, fix valid issues, and verify the changes with
appropriate checks and fresh CI. Do not request or wait for re-review of fixes
unless the user explicitly asks.

The user selected this workflow after PR #15's full review found three minor
documentation issues, all fixed, and the follow-up review hit a roughly
50-minute quota wait. A skipped or rate-limited follow-up is not a completion
blocker. The initial review's scope and any unresolved findings must still be
reported accurately. CI and verification remain required; merge authorization
is separate.

The tradeoff is that CodeRabbit does not independently verify each fix. Focused
verification, CI, and the agent's assessment provide that evidence. This
supersedes the former repeated CodeRabbit review loop in AGENTS.md.
