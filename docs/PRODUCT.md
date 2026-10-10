# Product scope

## Agreed direction

Grove is a personal Google Drive sync client, similar in purpose to InSync.
It uses Go and targets Linux first, followed by Windows and macOS.

`grove` provides individual CLI operations such as `auth`, `sync`, and `status`.
`groved` will provide background orchestration after one-shot sync is reliable.
`grove fixture` implements disposable fixture management. `grove sync` supports
initial and incremental downloads, plus explicit `--two-way` uploads on populated
profiles. Profile conflict policy and candidate resolution are shared engine
operations; `status` and daemon command syntax remain unfinalized.

Development should prioritize fast iteration and meaningful tests, including a
dedicated Google test account with reproducible fixtures.

## Requirements to settle before affected implementation

- Deletion propagation and automatic conflict resolution.
- Selection and exclusion of remote folders and local paths.
- Mapping Drive duplicate names and filesystem-incompatible names.
- Google-native document representation, shortcuts, and shared content.
- Symlink handling and filesystem case sensitivity across platforms.

## Data safety

Sync must have explicit deletion and conflict semantics and survive interrupted
operations. Live tests use disposable data. Concrete recovery guarantees will
be specified before implementing mutations.

## Initial download behavior

The first sync slice downloads ordinary account-owned My Drive files and folders
into an absent or empty destination with one immutable profile binding. Repeating
an unchanged population performs no media transfers. Interrupted downloads resume
at file boundaries; existing completed files are hash-checked and preserved.
Local edits and unexpected files are errors. Changes to an established population
follow the incremental behavior below. Deletion propagation remains deferred.
Unsupported names, symlinks,
native documents, shortcuts, duplicates, shared drives, and non-owned files fail
preflight rather than being silently skipped.

## Incremental download behavior

Approved spec 000003 is implemented on `feat/incremental-download`. Reusing a
profile applies remote additions, updates, and safe renames/moves using persisted
remote identities and local content hashes. A changed version with unchanged
size/MD5 advances metadata without a media download. Local conflicts and remote
removals stop new operations during preflight. Explicit local uploads and
profile-scoped conflict policy are implemented in
[spec 000004](spec/000004-local-upload-design.md). Deletion propagation remains
deferred. Recovery may finish an already journaled operation before planning new
work.

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
competitors require manual recovery. Deletions and local renames remain deferred.
