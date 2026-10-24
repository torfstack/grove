# Local watching for the Linux daemon

Date: 2026-10-24. Status: approved by the user; implementation pending.

Fresh read-only builder check: "nothing important left"; remaining implementation
choices are explicitly delegated below.

## Intent and agreed direction

After systemd integration merged in PR #16 at `23c583b`, the user selected
local file watching as the next bounded slice. The user chose opt-in native
Linux watching through `watch_local`, defaulting to false, with debounced
sequential attempts and periodic scans retained for remote changes and missed
events. Both binaries continue to share the existing sync engine.

The user approved the complete written contract below. Implementation-specific
choices in the paired plan remain distinguishable from these requirements.

## Approaches

- **Native events plus periodic reconciliation (selected):** react promptly
  without repeatedly walking an idle tree. Requires recursive watch management,
  bounded event handling, and explicit failure handling.
- **Frequent local polling:** simpler event handling, but repeatedly scans the
  tree and trades idle work for detection latency. Not selected.

## Scope and configuration

Extend the strict version-1 daemon JSON object with an optional boolean
`watch_local`. Omitted or false preserves periodic-only behavior. Reject null,
strings, and duplicate fields. Configuration still requires restart.

Watching applies to the bound local destination in either existing sync mode.
Enabling it requires an existing destination directory at startup; finish an
initial sync first if the root is absent. Periodic-only startup behavior remains
unchanged.
It never grants upload or deletion authority: download mode retains its current
local-conflict behavior, and two-way mode retains all baseline, conflict,
revision, and Trash checks. No new rename semantics, ignore rules, Drive changes
feed, hot reload, multiple-profile process, IPC, or non-Linux watcher is added.
Watching does not make an actively changing tree a consistent snapshot.

Use fixed initial timing defaults: a one-second quiet period and a five-second
maximum debounce window measured from the first pending event. No additional
timing knobs are introduced in this slice. Debounce improves responsiveness;
it is not proof that a writer has finished, so existing engine checks remain
authoritative. A continuously edited file may still yield an attempt failure.

## Scheduling contract

Keep immediate startup sync, lifetime daemon exclusion, original binding
validation, per-attempt locks, cancellation, and cleanup from spec 000009.
Initialize watching before the startup attempt so changes during it are not
lost. A single scheduler invokes RunExisting; the event reader never invokes
the engine or mutates sync state.

Events are hints requesting a complete normal sync attempt, never an operation
plan or evidence of a deletion. Coalesce them into bounded pending state rather
than an unbounded list of paths. Drain events while the engine is running.
Events during an attempt request at most one debounced follow-up; events during
that follow-up may request another. No attempts overlap, and triggering never
cancels an active attempt.

Maintain a periodic deadline at startup and after each periodic attempt,
measured from completion using elapsed time. Event-triggered attempts must not
push an existing periodic deadline back. If both triggers are ready, run one
attempt satisfying both. If a periodic deadline passes during an event attempt,
that completed full attempt satisfies it and schedules the next periodic
deadline from completion. Continuous activity cannot starve remote polling.

After a failed or busy attempt, retain pending hints but do not allow another
event-triggered attempt before one configured interval has elapsed from that
completion. Events cannot create a tight retry loop against conflicts, unavailable
credentials, or uncertain transactions. A successful attempt clears that retry
restriction. Periodic retries retain existing behavior.

## Native watcher and filesystem boundaries

Watch destination directories recursively for namespace, content-write, and
relevant metadata changes, including atomic-save replacement and directory
creation/removal/moves. Do not subscribe to reads, opens, or read-only closes:
the engine's own scans must not trigger endless attempts. Do not follow symlinks
or traverse outside the original destination. Symlink hints may request a scan;
the engine continues to reject unsupported local entries.

Register each directory before enumerating its descendants. Register new or
moved-in subtrees and reconcile watch membership after attempts. Directory
registration and traversal must tolerate concurrent changes without treating
an incomplete walk as a complete watch set. A newly registered subtree marks
reconciliation pending, covering changes before descendant registration.
Watcher paths are not trusted handles for engine filesystem operations.

Grove's own destination writes can generate hints. Do not suppress all events
during sync, as that would hide real user edits. Accept a coalesced no-op
follow-up after Grove writes; an unchanged follow-up must settle back to waiting.
Metadata events matter for directory topology and supported local validation,
but reading/hash-checking unchanged content must not sustain a loop.

If the root disappears, moves, or changes identity, stop with a sanitized fatal
watcher error; never recreate it or silently watch a replacement. Watch setup,
permission, watch-limit, and terminal reader failures also stop visibly rather
than pretending requested watching works. Existing engine validation remains
required before every mutation, including races before watcher failure detection.

Queue overflow means incomplete observation: discard detailed event assumptions,
request a complete sync, and rebuild/reconcile the recursive watch set. Drain
concurrently during rebuilding and retain a dirty generation for changes during
the rebuild. If full coverage cannot be re-established, stop visibly. Overflow
alone never authorizes a deletion. Periodic scans remain required even with an
apparently healthy watcher. Watchers are best effort on filesystems that do not
reliably report native events; document this limitation.

## Status, errors, and shutdown

Extend the private advisory record and public offline status with the configured
watching flag. In waiting state, `next_attempt_at` is the earliest currently
scheduled attempt across periodic, debounce, and retry restrictions. It is an
advisory timestamp: a later event may change it. Persist scheduling changes when
the effective deadline changes, after coalescing; do not write status per raw
event. Status remains offline and historical, with lock probes as the ownership
source. Do not record event paths, content, credentials, or Drive IDs.

Fatal watcher failures return a fixed sanitized error and preserve the latest
attempt result; process exit and journal output make failure visible. Historical
status is not a watcher-health promise. Status-persistence failures remain fatal.
Pre-release format changes need no migration, but malformed records must still
fail safely. Final field names and record version are builder choices.

On SIGINT/SIGTERM, stop scheduling, cancel and await active engine cleanup,
close and join the watcher reader, and release lifetime ownership only after
cleanup. Watcher shutdown must unblock a waiting reader. Preserve cancellation
identity and genuine cleanup errors; no leaked goroutines, descriptors, watches,
or helper processes. Systemd unit semantics remain unchanged.

## Implementation boundaries

Keep native event acquisition behind a Linux boundary and inject watcher events
and elapsed timers for deterministic scheduler tests. Reuse the existing Go
stack; selecting a maintained Go wrapper or existing x/sys native bindings is
left to the builder. Keep the adapter narrow rather than adding a general
event framework. The scheduler owns deadlines and coalescing; the adapter owns
recursive coverage and native resource cleanup. The shared planner and mutation
semantics should not need changes for this feature.

## Acceptance

- Strict config tests prove omitted/false behavior and invalid values.
- Deterministic scheduler tests prove debounce, maximum delay, periodic fairness,
  coalescing during attempts, retry restrictions, accurate advisory deadlines,
  cancellation, watcher failure, and no overlapping calls.
- Linux filesystem tests prove nested edits, atomic saves, new/moved directories,
  removal hints, metadata changes, no symlink traversal, root replacement,
  overflow reconciliation through injection, resource failure, and reader cleanup.
- Integration tests prove that Grove-generated writes settle after a bounded
  follow-up, and edits during sync are eventually scanned. Use a fake Drive
  endpoint and actual daemon process where lifecycle behavior matters.
- Run fmt, lint, offline tests, race tests, and both native builds. Verify
  periodic-only behavior remains intact. Report any unavailable checks.
- Add explicitly gated dedicated-profile live acceptance for a local edit in
  two-way mode triggering before a deliberately long periodic interval, verified
  independently on Drive, followed by a no-op cycle and fixture-owned cleanup.
  Live execution requires explicit enablement; no ordinary user data is used.
- Update operator/config examples, product, architecture, roadmap, testbed, and
  status to state implemented behavior and actual verification limits.

The [paired implementation plan](../implementation-plans/000011-local-watching-plan.md)
is prepared for review. No implementation is claimed yet.
