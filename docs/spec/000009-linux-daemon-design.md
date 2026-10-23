# Minimal Linux daemon with periodic sync

Date: 2026-10-23. Status: draft for user review; implementation not started.

## Intent and agreed direction

Make an existing Grove sync profile run periodically without repeated manual
commands. The first `groved` slice uses the existing shared engine, prevents
overlapping writers, shuts down safely, and exposes useful offline status.

The user selected periodic background sync for one configured profile and chose
a small configuration file over repeated command-line flags. Linux is the first
target. File watching and systemd installation follow later. Those choices are
agreed; the detailed defaults and behavior below are proposals for this review.
Work is on `feat/linux-daemon`.

## Approaches

- **Selected: small configuration file.** The daemon reads its settings once at
  startup. This supports repeatable launches and later systemd integration
  without repeating settings in a long command.
- **Command-line settings.** Similar to `grove sync` and initially simpler, but
  duplicates settings in each launcher. The user selected the file instead.

JSON, a five-minute default interval, and a reference to the existing profile
are the author's proposed implementation choices. JSON matches Grove's private
record format and requires no additional parsing dependency. Referencing the
profile avoids duplicating its immutable binding or storing credentials in the
daemon configuration.

## Scope and constraints

- Add a foreground `groved` process and `grove status` command on Linux. The
  process runs until interrupted; detaching from the shell is left to the
  operator or a future service manager.
- Run one existing profile per process. Multiple independent processes may
  operate different profiles through existing destination/token exclusion.
- Call the Go sync service directly. Do not spawn `grove sync`, build a second
  engine, or add new Drive mutation/recovery semantics.
- Preserve download-only as an available mode and require an explicit config
  choice for two-way sync. Keep existing conflict policies and explicit deletion
  resolution rules; the daemon never invents a conflict choice.
- No initial profile creation, profile rebinding, interactive authentication,
  hot reload, filesystem watching, changes-feed cursors, cron schedules, IPC,
  systemd installation, or non-Linux runtime support in this slice.
- Grove is pre-release; old-release migrations are unnecessary. Preserve
  current-run data safety, journals, ownership checks, and interruption recovery.
- Keep credentials, generated Drive IDs, filenames, synced contents, and private
  stage details out of logs and public status output.

## Configuration and launch

Launch with an explicit `groved --config /absolute/path/daemon.json`; the config
argument must be absolute. Do not search the current directory or a default
profile. A minimal example is:

```json
{
  "version": 1,
  "profile_dir": "/home/example/.local/state/grove/example-profile",
  "mode": "download",
  "interval": "5m"
}
```

The path is illustrative, not an existing fixture or generated binding.

| Field | Proposed contract |
| --- | --- |
| `version` | Required integer `1`; reject other versions. |
| `profile_dir` | Required absolute path to an existing Grove profile; resolve aliases before locking. |
| `mode` | Required string `download` or `two-way`; no implicit permission upgrade. |
| `interval` | Optional duration string such as `30s`, `5m`, or `1h`; default `5m`, minimum `1s`. Reject invalid, overflowing, or smaller durations. |

Read a regular, nonsymlink, current-user-owned configuration file with owner-only
permissions, outside Git and the synchronized destination. Limit its size to
64 KiB and require valid UTF-8 JSON. Reject unknown fields, duplicate fields,
wrong types, missing required fields, multiple JSON values, and trailing
non-whitespace data. Do not expand
`~`, environment variables, or relative paths inside JSON. Report field names
and fixed reasons without echoing values or file contents.

Read configuration once, validate it, and retain the parsed settings for the
process lifetime. Changes take effect only after restart. Reject a config path
that aliases `state.json`, `profile.lock`, `daemon.lock`, or `daemon-status.json`
inside the selected profile; daemon operation must never overwrite its config.

Require an existing profile directory and regular state file before creating
the lifetime guard file. After acquiring lifetime ownership, validate the
profile under its profile lock before writing daemon status or opening an
OAuth session. Startup profile contention is a nonzero startup failure, not
permission to inspect unlocked state; the operator can start again after the
manual operation ends. Download mode may resume an existing incomplete initial
profile.
Two-way mode requires an already populated profile, matching the existing sync
contract; an incomplete cloud restoration must first resume in download mode.
Missing or invalid configuration/profile state is a startup failure, not
permission to create a new profile.

## Existing-profile service boundary

The existing `Service.Run` accepts explicit binding paths and can initialize a
new profile. Add a narrow shared-engine path that accepts the existing profile
location and mode, resolves its saved binding, validates it through the normal
state/binding rules, and runs with that same profile lock still held. The daemon
must not decode private state independently, trust a cached binding after the
profile disappears, or check for existence and then call a path that can create
a new profile after a race.

Preserve the existing CLI API and engine behavior. Retain the validated startup
binding as an expected identity, and compare each freshly loaded binding against
it while holding the profile lock. A profile deleted, malformed, or rebound
between attempts yields an attempt failure; it is never silently recreated or
rebound. The remembered binding is a comparison value, not authority to proceed
when current profile state is missing. Keep canonical profile, destination, registry, and token
exclusion checks. A profile-specific offline inspection path may reuse the same
validated state loading for status, without opening authentication or Drive.

## Scheduling and outcomes

After validation, lifetime-lock acquisition, and initial status persistence,
start one sync immediately. After each completed attempt, wait one full configured
interval before the next attempt. Measure runtime waits with an elapsed-time
clock; UTC timestamps are informational. A slow attempt does not accumulate
missed ticks or trigger catch-up runs. Never run two attempts concurrently.

Each attempt reloads the validated existing profile through the shared service.
Pass `TwoWay=false` for `download` and `TwoWay=true` for `two-way`. Continue to
use complete scans and existing engine recovery before new planning. A pending
uncertain remote write is not cleared, blindly retried, or reassigned an ID by
the scheduler. Configured conflict policy remains the engine's responsibility.

An attempt may return completed operation counts and still leave conflicts.
"Completed" describes an engine invocation returning successfully; it does not
promise the profile is fully synchronized. Status reports conflicts and recovery
separately when its offline profile snapshot is available.

On an ordinary sync error, record a fixed failure category and wait the normal
interval before another attempt. Do not add exponential backoff or immediate
outer retries; the existing Drive client keeps its own bounded read retries.
The repeated invocation uses current durable engine state and must retain its
uncertain-write protections. Persistent auth errors, local conflicts, or blocked
recovery remain visible and may fail repeatedly until the operator intervenes.
The daemon never opens a browser or automatically changes config or policy.

Distinguish genuine profile/destination/token contention from I/O or validation
errors with a typed shared lock-busy error, not matching error strings. Busy
attempts are recorded as `busy`, perform no overlapping work, and wait the same
interval. This applies to attempts after validated startup, not startup profile
inspection. Unclassified failures are `sync_failed`; record no raw wrapped error
text in daemon status or logs. Startup/configuration failures, runtime status
persistence failures, and daemon-owned lock-release errors are fatal and exit
nonzero. Engine errors retain the normal attempt-failure behavior above.

## Ownership, manual commands, and shutdown

Hold a canonical `<profile>/daemon.lock` for the process lifetime, separate from
the engine's per-attempt `profile.lock`. A second daemon for the same canonical
profile fails before opening credentials, changing status, or syncing, even if
it uses a different config file or profile-path alias.

Acquire the daemon lock before any profile, destination, or token lock. Release
startup inspection's profile lock before entering the service that acquires it;
do not recursively acquire the engine lock. The existing-profile service keeps
its own profile lock throughout each attempt. Release per-attempt resources
between attempts. Manual sync, conflict resolution, recovery, and authentication
remain usable while idle; overlapping operations obey existing nonblocking
exclusion. Different profiles sharing a token still serialize token sessions.

On SIGINT or SIGTERM, stop scheduling, cancel the active engine context, and
wait for the attempt and resource cleanup to return before releasing lifetime
ownership. Do not terminate a live worker goroutine and let a replacement writer
start against it. Preserve the engine's normal interruption/journal behavior.
Record a stopped state when possible, then exit zero for a clean requested stop;
status or cleanup errors produce a sanitized nonzero exit. There is no separate
force-stop timeout or signal-triggered reload in this slice.

## Offline status

Add `grove status --profile-dir /absolute/path [--json]`. It needs no config
file, token, network connection, daemon RPC, or Drive access. Human and JSON
forms expose the same facts:

- whether a daemon currently holds lifetime ownership: active, inactive, or
  unknown if ownership cannot be inspected;
- the last recorded daemon phase and update timestamp;
- last attempt start/finish, outcome, operation counts, configured mode/interval,
  and the recorded next-attempt timestamp when waiting;
- current offline profile summary: population complete, conflict count, pending
  recovery flag, or unavailable/busy if it cannot be read consistently.

Persist version-1 advisory daemon state atomically at
`<profile>/daemon-status.json`, in the private profile directory with file mode
0600. Record phases `starting`, `syncing`, `waiting`, `stopping`, and `stopped`;
attempt outcomes `never`, `completed`, `busy`, `failed`, and `cancelled`. Include
a local session identifier and UTC timestamps, but do not print the session ID,
remote identities, token paths, or raw errors. Use fixed reason categories.

Lifetime ownership must be determined from the lock, not a PID or cached phase.
Use a non-mutating shared lock probe so concurrent status readers cannot mistake
one another for a daemon. A missing lifetime-lock file means inactive; permission
and inspection failures mean unknown, never inactive or active by assumption.
The cached record is explicitly historical: label its phase and next-attempt
time as last recorded, not proof of present progress or a promise to run. This
also covers the startup/shutdown windows between a record write and lock change.
A crash may leave `syncing` in the record while ownership is inactive; report
that combination without clearing or adopting engine intent.

Read the profile summary under its nonblocking profile lock. If an engine
attempt owns it, report busy/unknown rather than reading partially changing
state or inventing zero conflicts. Guard-file operations are allowed, but status
does not mutate sync state, refresh tokens, create a profile, or clean artifacts.
No daemon status file is a normal "never recorded" condition. A malformed or
unsupported daemon record is reported as unavailable and makes status exit
nonzero; do not print its contents. Invalid/missing profiles and inspection I/O
failures also exit nonzero; valid inactive/busy observations exit zero.

Daemon status must be writable before starting each attempt. If the post-attempt
write fails, preserve the engine's committed work and journals, stop scheduling,
and exit nonzero; do not roll back or repeat a completed attempt because its
status could not be written. Stale status is advisory and never authorizes a
sync mutation, recovery reset, conflict resolution, or deletion.

## Components and builder discretion

Add `cmd/groved` and a small orchestration package for strict config loading,
the sequential loop, lifetime ownership, and advisory status. Keep Drive and
sync planning/execution in the existing shared engine. Extend the existing CLI
with offline status through injected services. Reuse private atomic records and
locking boundaries, adding only the narrow existing-profile and lock-probe
capabilities this slice requires. Keep Linux signal/lock behavior behind the
existing platform boundaries. Unsupported platforms must fail before sync.

The builder chooses package names, exact helper signatures, the internal status
record layout, and clock injection mechanics. Use the pinned Go toolchain and
standard library/existing dependencies. Avoid unrelated refactors, daemon IPC,
a general job scheduler, and comprehensive error-taxonomy work beyond a typed
lock-busy distinction and fixed daemon outcome reasons.

## Verification and acceptance

Deterministic tests with a controlled clock and injected sync service cover:

1. Strict config parsing, defaults, explicit modes, path/permission validation,
   duplicate/unknown fields, unsupported versions, size limits, and no mutation
   before invalid configuration is rejected. Valid configuration with invalid
   profile state may create guard files for validation, but no status/sync state
   writes or credential access before that profile is rejected.
2. An immediate first attempt, wait-after-completion scheduling, slow attempts,
   no catch-up or overlap, configured mode forwarding, and config changes taking
   effect only after restart.
3. Error and busy outcomes waiting normally, no scheduler-driven write retry,
   and fail-closed behavior for missing, malformed, or rebound profiles between
   attempts. Assert profile resolution and execution share the lock.
4. Two processes/aliases contending for one daemon profile; manual sync,
   recovery, and token contention; different profiles sharing a token; separate
   profile/destination protections remaining intact.
5. SIGINT/SIGTERM and cancellation during startup, wait, transfer, and pending
   recovery; no new attempt after cancellation, no early lifetime-lock release,
   and restart through durable engine recovery. Use subprocess tests where
   actual process locks/signals matter and deterministic engine save faults for
   transfer/publication boundaries.
6. Offline status without credentials/network, concurrent status readers,
   lock-inspection errors, busy profile snapshots, stale/crashed records,
   malformed records, and PID reuse being irrelevant to ownership detection.
7. Status-save failures before and after sync, cleanup errors, partial operation
   counts, unresolved conflicts, and sanitized human/JSON output. Verify that
   status never mutates sync intent or resolves a conflict.

Implementation verification includes formatting, lint, full offline/race tests,
native Linux builds of both binaries, and CI coverage for both binaries. Extend
the opt-in dedicated-account live acceptance with an owned fixture profile:
run initial one-shot sync, launch the real daemon in download mode, independently
verify an owned remote edit is downloaded on a subsequent attempt, verify a
no-op cycle and offline status, stop cleanly, and confirm owned cleanup. Include
an explicit two-way daemon case for an owned local edit and its independently
verified upload. Bound live waits and clean only fixture-owned data; failed runs
retain recovery records. Offline tests, not timing-sensitive live tests, prove
the failure/recovery invariants. No live run is authorized merely by this draft.

After approval, write the paired plan at
`docs/implementation-plans/000009-linux-daemon-plan.md`. Update product,
architecture, roadmap, status, and testbed documentation with implemented
behavior and actual results during execution.
