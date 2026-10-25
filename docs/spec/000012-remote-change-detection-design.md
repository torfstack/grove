# Remote change hints for the Linux daemon

Date: 2026-10-25. Status: authorized direction; detailed implementation choices
below are made by the agent under the user's standing instruction to continue
autonomously. Implementation and dedicated-account live acceptance are complete; final
verification/review precede integration. A fresh read-only builder check reported
"nothing important left".

## Intent

After successful dedicated-account live watcher acceptance, the user authorized
remote change detection so cloud edits can trigger sync sooner than the normal
full-scan interval. Keep existing complete scans, revision/conflict/Trash rules,
profile binding, single-writer exclusion, and interruption recovery authoritative.
A changes-feed response is a hint, never a sync plan or deletion authorization.

## Approaches and choice

- **Poll Drive's changes feed (agent selection):** cheap account-level checks
  request the existing engine when changes occur. Requires cursor/pagination,
  token coordination and failure handling, but avoids frequent idle tree scans.
- **Shorten the existing full-sync interval:** no new endpoint, but repeatedly
  lists remote trees and hashes local files even when idle.
- **Push notifications:** requires reachable HTTPS/channel lifecycle infrastructure
  beyond this local Linux daemon slice.

## Configuration and scope

Add optional `remote_poll_interval` to strict version-1 daemon JSON. Absence means
remote polling is disabled. When present, require a duration string of at least
10 seconds; examples use `"30s"`. Reject null, booleans, malformed/overflowing
values and duplicate fields. Configuration changes still require restart.

Support polling with either existing mode, with or without watch_local. Polling
uses read-only authorization and never broadens mutation authority. Native
watching alone retains its existing-root requirement; remote-only polling adds
no new local-root requirement. Both features disabled preserves the periodic-only
path. No dependencies, webhook server, shared-drive support, persistent feed
cursor, subtree event filter, or engine/planner mutation changes are introduced.

The first slice deliberately treats any returned My Drive change as a hint,
including changes outside the selected root and Grove's own writes. False-positive
no-op syncs are acceptable. It is not an event-by-event or instantaneous guarantee.

## Cursor and HTTP contract

Use a separate narrow Drive changes interface so existing API implementers do
not acquire unused methods. Get an opaque starting cursor from
changes.getStartPageToken. List changes with a nonempty page token, spaces=drive,
restrictToMyDrive=true, includeRemoved=true, and shared-drive inclusion disabled.
Request only cursor fields and minimal change-entry data; no event paths or file
IDs are needed. Do not log or expose cursors, credentials, IDs or response bodies.

Keep the cursor in daemon memory only. Initialization always emits a dirty hint
after obtaining a cursor, ensuring a normal sync occurs after that observation.
If initialization runs before startup sync, startup can satisfy it; otherwise a
follow-up closes the startup observation gap. Restart obtains a new cursor and
retains immediate startup reconciliation; no persisted profile migration is needed.

Traverse all pages before accepting a new start cursor. Retain the original
cursor on transport/JSON/status/shape errors or interrupted pagination. A final
page must supply newStartPageToken and no nextPageToken; intermediate pages must
supply nextPageToken and no terminal cursor. Detect repeated continuation tokens,
empty required tokens, overlong tokens (>64 KiB), responses over 1 MiB, and more
than 100 pages. Ordinary fields beyond the requested shape can be ignored.
A change observed before a later page failure still requests a normal sync; it
does not authorize cursor advancement. Empty feed results never authorize deletion.

Use the existing read-retry/redirect policy, bounded by a ten-second context for
the whole check, including local coordination. Return context identity and genuine
close errors through sanitized wrappers. Provider-rejected cursors retain the
original value and visible failed-check status; periodic scans continue. Restart
is the initial operator recovery; no unsafe automatic cursor reset is added.

## Scheduling and token coordination

Keep immediate startup sync and independent periodic deadlines. Poll at startup,
then wait the configured remote interval after each completed/busy/failed check.
A remote hint enters the same one-second quiet/five-second maximum debounce and
failed/busy sync retry restriction as local hints. Local, remote and periodic
triggers coalesce into one normal sequential engine run. Poll results cannot
postpone periodic sync or cancel an active engine attempt.

Serialize each remote check and RunExisting call through one context-aware
in-process gate. This gate spans service cleanup, not just HTTP work, so the
same daemon cannot contend with its own token/profile lease. Polling waits for
active sync only within its ten-second budget, then reports busy. Sync can wait
for an already active bounded check; it is never abandoned or marked busy merely
because its own poller has the token. Other processes retain normal exclusion.
Polling may therefore add up to a check's remaining budget before an engine call
can proceed. No attempt calls overlap.

Each check uses the shared service's locked existing-profile boundary: validate
and compare the original binding before authentication, open a read-only API
session, check the feed, and close session/profile leases before releasing the
gate. Do not create missing profiles or use a rebound token. No destination or
baseline writes occur. OAuth refresh still uses existing atomic token persistence.

Ordinary authentication, resource-busy, transport, malformed-feed and API failures
are historical check failures retried at the remote interval; existing periodic
sync continues. Genuine session/lease cleanup failure is a typed fatal fault,
joined with cancellation or other causes. Native watcher/root fatal behavior is
unchanged. Unknown or incomplete observations never permit a new mutation.

## Status and lifecycle

Add configured remote interval, last remote-check completion timestamp and outcome
(never/completed/busy/failed) to the private advisory record and public historical
status. Disabled records use zero interval, no timestamp and empty outcome;
enabled startup uses never and no timestamp. Other outcomes require a valid UTC
timestamp. No cursor or raw service error appears in status.

The scheduler alone writes records. A bounded source mailbox carries first/latest
hint times, the latest completed check observation, and sticky fatal errors.
Persist one observation per coalesced check completion without resetting sync
attempt counts/history. Existing next_attempt_at continues to describe the next
normal sync, not the next cheap remote check. Ownership remains lock-observed;
recorded polling configuration/outcomes are not liveness or current health proof.

Shutdown cancels engine and remote-check contexts, waits for engine cleanup,
closes/joins all hint sources, drains sticky failures after join, and only then
releases lifetime ownership. Suppress cancellation-only outcomes; preserve genuine
reader/check/cleanup errors even when concurrent with SIGINT/SIGTERM. A remote
poll goroutine cannot outlive its daemon or retain a token lease after shutdown.

## Verification

- Fake HTTP tests cover cursor initialization, escaped opaque tokens, pagination,
  empty pages, removal hints, malformed/oversized responses, duplicate page tokens,
  page caps, partial progress, cancellation and sensitive-data redaction.
- Locked-service tests cover missing/rebound profiles before authentication,
  read-only access, profile/token contention, unchanged state/binding, and genuine
  API/profile cleanup failures joined with operation cancellation.
- Deterministic source/scheduler tests cover polling intervals, coalesced hints,
  non-overlapping sync/check work, local/remote/periodic fairness, sync cooldown,
  ordinary failure retries, historical status and cancellation/late cleanup faults.
- Real-process fake-provider acceptance covers remote changes triggering download
  before a long periodic deadline, no-op settling and signal/lease cleanup, with
  local-only/periodic-only behavior retained.
- Run pinned formatting, lint, offline/race tests and Linux builds; cross-build
  both binaries for macOS to enforce platform boundaries. Use direct pinned task-
  equivalent commands locally to avoid the known host mise auto-cleanup side effect;
  hosted CI continues using the repository's mise tasks.
- Add and explicitly run dedicated-account live remote-triggered download with
  disposable profiles, independent bytes/no-op verification, offline status,
  shutdown and fixture-owned cleanup. Existing live watcher acceptance remains a
  regression check. Never retry an uncertain mutation or inspect unrelated data.
- Update product, architecture, roadmap, testbed, status and decision records with
  actual results, reviewed commits, limitations and delegated agent choices.

## References

- [Retrieve changes](https://developers.google.com/workspace/drive/api/guides/manage-changes)
- [changes.list](https://developers.google.com/workspace/drive/api/reference/rest/v3/changes/list)
- [changes.getStartPageToken](https://developers.google.com/workspace/drive/api/reference/rest/v3/changes/getStartPageToken)

The [paired implementation plan](../implementation-plans/000012-remote-change-detection-plan.md) defines six native execution tasks.
