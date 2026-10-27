# Product scope

## Agreed direction

Grove is a personal Google Drive sync client, similar in purpose to InSync.
It uses Go and targets Linux first, followed by Windows and macOS.

`grove` provides individual CLI operations such as `auth`, `sync`, and `status`.
`groved` provides foreground periodic sync for one existing Linux profile.
`grove fixture` implements disposable fixture management. `grove sync` supports
initial and incremental downloads, plus explicit `--two-way` uploads and safe
local deletion propagation on populated profiles. Local deletions move matching
Drive items to Trash after baseline and subtree validation. Profile conflict
policy and candidate resolution are shared engine
operations. `grove status --profile-dir PATH [--json]` and
`groved --config PATH` are implemented and documented in
[spec 000009](spec/000009-linux-daemon-design.md).

Development should prioritize fast iteration and meaningful tests, including a
dedicated Google test account with reproducible fixtures.

## Proposed macOS desktop experience

The user selected a macOS experience parallel to Linux: a periodic foreground
`groved` process managed as an optional per-profile launchd user agent, plus a
native menu-bar companion for offline status and service controls. Installation,
daemon login start, and menu-bar login start remain explicit user choices. This
scope is approved in [spec 000013](spec/000013-macos-daemon-design.md), and its
[implementation plan](implementation-plans/000013-macos-daemon-plan.md) is now
being implemented. Local filesystem watching is not part of this slice.

## Requirements to settle before affected implementation

- Automatic conflict resolution beyond the existing per-profile policies.
- Selection and exclusion of remote folders and local paths.
- Mapping Drive duplicate names and filesystem-incompatible names.
- Google-native document representation, shortcuts, and shared content.
- Symlink handling and filesystem case sensitivity across platforms.

## Data safety

Sync must have explicit deletion and conflict semantics and survive interrupted
operations. Live tests use disposable data. Local deletions propagate only during
explicit two-way sync and move unchanged Drive items to Trash. Changed remote
subtrees remain pending until the user explicitly chooses local deletion or
cloud restoration.

## Initial download behavior

The first sync slice downloads ordinary account-owned My Drive files and folders
into an absent or empty destination with one immutable profile binding. Repeating
an unchanged population performs no media transfers. Interrupted downloads resume
at file boundaries; existing completed files are hash-checked and preserved.
Local edits and unexpected files are errors. Changes to an established population
follow the incremental behavior below. Local deletion propagation is implemented
according to
[spec 000007](spec/000007-deletion-propagation-design.md).
Unsupported names, symlinks,
native documents, shortcuts, duplicates, shared drives, and non-owned files fail
preflight rather than being silently skipped.

Download verification and recovery accept a metadata-version increase only when
the same binary revision, modified time, size/checksum, identity, type, parent,
ownership, active status, and download capability are proven unchanged. If
revision/time evidence is absent, version equality remains required. Actual
downloaded bytes must still pass checksum verification. Saved journal snapshots
remain intact; a later scan can reconcile a newer version without downloading
again or changing destination-file mtimes. See
[spec 000008](spec/000008-download-metadata-version-design.md).

## Incremental download behavior

Approved spec 000003 is implemented in main. Reusing a profile applies remote additions, updates, and safe renames/moves using persisted
remote identities and local content hashes. A changed version with unchanged
size/MD5 advances metadata without a media download. Local conflicts and remote
removals stop new operations during preflight. Explicit local uploads and
profile-scoped conflict policy are implemented in
[spec 000004](spec/000004-local-upload-design.md). Recovery may finish an
already journaled operation before planning new work.

An unchanged run performs no destination writes or media transfers. Replacements
retain verified old content until the new baseline is durable; the local target
may briefly be absent. Unsupported native move primitives, occupied targets,
path swaps/cycles, and same-run reuse of tracked vacated paths fail safely.

## Local upload behavior

[Spec 000004](spec/000004-local-upload-design.md) defines explicit `--two-way`
sync on populated profiles: new local files/folders and revision-aware local
content updates, retaining downloads and profile-scoped conflict policy shared by
the CLI and daemon. The choices are ask (default), keep-local, and
keep-cloud. Download-only remains the default. Drive does not enforce conditional
writes, and retained revision pins consume its per-file quota.
[Spec 000006](spec/000006-multiple-conflict-candidates-design.md) adds durable
capture of multiple cloud candidates, inspection through `conflicts show`, and
explicit cloud-candidate selection. Automatic policies leave multi-candidate
conflicts pending. Incomplete capture remains journaled for recovery.
Local renames remain deferred. Local deletion propagation is implemented according to spec 000007.
Independent local and cloud additions at the same path remain pending under
keep-local; this slice supports choosing cloud for those conflicts. Uncertain
remote writes preserve their journal and may require operator reconciliation.

## Operator recovery

[Spec 000005](spec/000005-operator-reconciliation-design.md) adds offline
`recovery inspect`, recovery-only `recovery retry`, and private verified-stage
`recovery export`. Retry never starts new sync work or applies conflict policy.
Export copies bytes without clearing intent. Uncertain writes can remain blocked
indefinitely; force-abandon and unrecorded artifact reconciliation remain
deferred. Multiple preserved cloud candidates can be inspected and selected
explicitly under spec 000006.

## Periodic Linux sync

[Spec 000009](spec/000009-linux-daemon-design.md) adds foreground `groved` for
one existing profile and offline `grove status`. A private JSON config requires
explicit download or two-way mode and defaults to five minutes between completed
attempts. Configuration changes require restart. Status separates lock-observed
ownership from historical attempts and unavailable/busy profile summaries.
Signal shutdown preserves the shared engine's recovery obligations; conflicts
and uncertain mutations keep their existing operator workflow.
[Spec 000010](spec/000010-systemd-user-service-design.md) supplies a systemd
user-service template and installation instructions for named config instances.
Users explicitly start/enable services; operation after logout is an optional
linger setting. Fatal failures stay stopped for inspection and graceful stop
waits for cleanup without a forced timeout. [Spec 000011](spec/000011-local-watching-design.md) adds opt-in `watch_local`
Linux events with one-second quiet/five-second maximum debounce and periodic
scans retained. Events request sequential normal engine runs and do not grant
mutation authority. Failed/busy event retries wait one interval, and fatal watch
coverage/root failures stop visibly. Multiple profiles per process, hot reload,
and other native platform lifecycles remain follow-ups.

## Remote change hints

[Spec 000012](spec/000012-remote-change-detection-design.md) adds opt-in
`remote_poll_interval` (minimum ten seconds) for read-only My Drive change hints.
Polling and normal engine calls serialize through cleanup. Native local watching
is independent, and full periodic scans remain enabled. Any account-level change
may request a no-op scan, including activity outside the selected root.

Cursors are ephemeral and initialization requests a later normal scan. Complete
pagination is required to advance them; partial changes can request a scan while
errors retain the original cursor. Feed removals never authorize deletion. Normal
check failures retry and are historical status; cleanup faults stop visibly.
No new remote-removal propagation or sync mutation semantics are introduced.


## Linux desktop indicator

[Spec 000014](spec/000014-linux-freedesktop-tray-design.md) adds `grove-tray`, a
separate per-profile companion using StatusNotifierItem and D-BusMenu. The icon
and host-rendered menu show offline status and preserve the distinction between
live ownership, historical attempts, and busy/unavailable profile details.
It remains available while the daemon is stopped. Optional explicitly linked
systemd user-service controls validate the effective unit command and profile
configuration before start/stop. Quitting the tray leaves the daemon running.
Installation and graphical-session autostart are manual. Waybar is verified;
other compatible hosts need independent acceptance before a tested-support claim.
