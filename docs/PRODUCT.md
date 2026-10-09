# Product scope

## Agreed direction

Grove is a personal Google Drive sync client, similar in purpose to InSync.
It uses Go and targets Linux first, followed by Windows and macOS.

`grove` provides individual CLI operations such as `auth`, `sync`, and `status`.
`groved` will provide background orchestration after one-shot sync is reliable.
Command syntax beyond these names is not finalized.

Development should prioritize fast iteration and meaningful tests, including a
dedicated Google test account with reproducible fixtures.

## Requirements to settle before affected implementation

- Initial sync direction and how existing local files are treated.
- Conflict resolution, deletion propagation, and recovery behavior.
- Selection and exclusion of remote folders and local paths.
- Mapping Drive duplicate names and filesystem-incompatible names.
- Google-native document representation, shortcuts, and shared content.
- Symlink handling and filesystem case sensitivity across platforms.

## Data safety

Sync must have explicit deletion and conflict semantics and survive interrupted
operations. Live tests use disposable data. Concrete recovery guarantees will
be specified before implementing mutations.
