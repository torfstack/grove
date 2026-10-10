# Product scope

## Agreed direction

Grove is a personal Google Drive sync client, similar in purpose to InSync.
It uses Go and targets Linux first, followed by Windows and macOS.

`grove` provides individual CLI operations such as `auth`, `sync`, and `status`.
`groved` will provide background orchestration after one-shot sync is reliable.
`grove fixture` and `grove sync` now implement disposable fixture management and
initial and incremental downloads; `status` and daemon command syntax remain unfinalized.

Development should prioritize fast iteration and meaningful tests, including a
dedicated Google test account with reproducible fixtures.

## Requirements to settle before affected implementation

- Upload direction and automatic treatment of local edits/conflicts.
- Conflict resolution, deletion propagation, and recovery behavior.
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
Local edits, unexpected files, and changes to a completed remote population are
errors. There is no overwrite or deletion propagation. Unsupported names, symlinks,
native documents, shortcuts, duplicates, shared drives, and non-owned files fail
preflight rather than being silently skipped.

## Incremental download behavior

Approved spec 000003 is implemented on `feat/incremental-download`. Reusing a
profile applies remote additions, updates, and safe renames/moves using persisted
remote identities and local content hashes. A changed version with unchanged
size/MD5 advances metadata without a media download. Local conflicts and remote
removals stop new operations during preflight. Uploads, deletion propagation,
and automatic conflict resolution remain deferred. Recovery may finish an
already journaled operation before planning new work.

An unchanged run performs no destination writes or media transfers. Replacements
retain verified old content until the new baseline is durable; the local target
may briefly be absent. Unsupported native move primitives, occupied targets,
path swaps/cycles, and same-run reuse of tracked vacated paths fail safely.
