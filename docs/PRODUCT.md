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
on `feat/deletion-propagation` according to
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

Approved spec 000003 is implemented on `feat/incremental-download`. Reusing a
profile applies remote additions, updates, and safe renames/moves using persisted
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
the CLI and future daemon. The choices are ask (default), keep-local, and
keep-cloud. Download-only remains the default. Drive does not enforce conditional
writes, and retained revision pins consume its per-file quota; multiple concurrent
competitors require manual recovery. Local renames remain deferred. Local deletion
propagation is implemented according to spec 000007.
Independent local and cloud additions at the same path remain pending under
keep-local; this slice supports choosing cloud for those conflicts. Uncertain
remote writes preserve their journal and may require operator reconciliation.

## Operator recovery

[Spec 000005](spec/000005-operator-reconciliation-design.md) adds offline
`recovery inspect`, recovery-only `recovery retry`, and private verified-stage
`recovery export`. Retry never starts new sync work or applies conflict policy.
Export copies bytes without clearing intent. Uncertain writes can remain blocked
indefinitely; force-abandon, unrecorded artifact reconciliation and selection
among multiple competitors remain deferred.

## Periodic Linux sync

[Spec 000009](spec/000009-linux-daemon-design.md) adds foreground `groved` for
one existing profile and offline `grove status`. A private JSON config requires
explicit download or two-way mode and defaults to five minutes between completed
attempts. Configuration changes require restart. Status separates lock-observed
ownership from historical attempts and unavailable/busy profile summaries.
Signal shutdown preserves the shared engine's recovery obligations; conflicts
and uncertain mutations keep their existing operator workflow. File watching,
systemd installation, multiple profiles per process, and other native platform
lifecycles remain follow-ups.
