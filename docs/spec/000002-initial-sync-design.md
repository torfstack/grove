# Initial download sync and Drive fixtures

Date: 2026-10-10. Status: approved; implemented with offline verification and macOS live acceptance.

## Intent and agreed scope

Deliver a repeatable seed → download → verify → repeat workflow for a dedicated
disposable Drive account. Fixture tooling, the shared initial download engine,
CLI, tests, and documentation belong on one branch, `feat/initial-sync`.

The user agreed to ordinary files and folders, an empty initial destination, and
deferring Google-native documents, shortcuts, and duplicate sibling names.
Success means independently verified content, no redundant media transfers on
an unchanged second run, and safe recovery after an interrupted download.
The user approved this written design. The behavior and interfaces below are
requirements for implementation, not implemented commands.

## Approach

Build the fixture helper first, then the download engine, keeping both in the
same feature branch. The helper calls the Drive adapter directly; it never uses
the sync planner or executor to seed or verify expected content. This provides
an independent oracle without duplicating OAuth and HTTP infrastructure.

A permanently pre-uploaded fixture would be simpler initially but permits drift
and interference between runs. Seeding through a future upload sync engine would
couple the test to another unimplemented subsystem. A fresh API-seeded tree per
live run is the recommended approach.

## Command surface

Proposed commands:

```text
grove fixture seed --manifest PATH --run-dir PATH --token-file PATH
grove fixture inspect --run-dir PATH --token-file PATH
grove fixture verify --manifest PATH --local-dir PATH
grove fixture cleanup --run-dir PATH --token-file PATH
grove sync --profile-dir PATH --remote-root ID --local-dir PATH --token-file PATH
```

Fixture seed requires a new run directory and creates one fresh root in the test
account's My Drive. Inspection checks the recorded remote membership and content
against the seed manifest. Verification walks local content independently and
requires exact membership, file types, sizes, and SHA-256 hashes; timestamps are
irrelevant. Inspect and sync suppress raw Drive IDs from console output. Seed
reports the private run-record path; the live harness reads its root ID directly.

Sync requires explicit profile, remote root, local destination, and token paths
in this first slice. These identify one immutable profile binding. Reusing a
profile with different bindings is an error. A new profile accepts an absent or
empty destination; subsequent invocations can resume that profile's work.

Commands report counts and sanitized errors, not credentials, remote IDs, HTTP
response bodies, or file contents. No automatic browser authentication or scope
upgrade occurs in fixture or sync commands.

## Fixture manifest and run ownership

Store the baseline manifest and small deterministic payloads under
`testdata/fixtures/baseline/`. Manifest version 1 has logical IDs, parent logical
IDs, names, folder/file types, and for files payload-relative paths, byte sizes,
and SHA-256 hashes. A distinguished logical root has no parent. Reject duplicate
IDs, missing parents, cycles, invalid names, duplicate sibling names, payload
escapes or symlinks, and mismatched sizes/hashes before any API mutations.

Include nested and empty folders, zero-byte files, UTF-8 text, binary bytes,
Unicode names, and enough files for deterministic offline pagination tests.
The live suite also forces a small API page size to exercise pagination.

The private versioned run record contains a random run identifier, a copy of the
validated manifest, creation status, logical-to-Drive-ID mapping, and per-object
cleanup progress. Create directories with 0700 and records with 0600. Persist
each confirmed create atomically before issuing the next create. Mark created
objects with app-private run and logical-entry properties.

Do not blindly retry an ambiguous create: stop and retain the record. Inspection
can reconcile an uncertain create using its random ownership properties and
expected parent; ambiguous matches are errors. If the root create outcome is
unknown, reconciliation may search the run property, never a folder name.
Recovered IDs must be persisted before further mutations.

Cleanup first resolves pending creates, validates recorded ownership and parent
relationships, and completes a full listing of the tree. Abort on unknown or
unrecorded descendants. Trash only individually recorded owned objects, children
before parents, recording progress so cleanup can be repeated. Missing objects
are already cleaned; permission and listing failures are errors. Never clean the
whole account or infer deletion authority from an incomplete listing. Concurrent
manual edits to the disposable fixture are outside the supported live workflow.

## Authentication and Drive boundary

Reuse the existing versioned auth record and Desktop client loader. Check that
the loaded client ID matches the record. Read-only sync accepts read-only or
read-write Drive scope; seed and cleanup require read-write. Tests use a dedicated
token path, not the default personal profile. Never read or print secrets during
repository inspection.

Add a persistent token source that atomically saves successful refreshes, retaining
the previous refresh token when omitted. Persistence failure fails the command.
Serialize refresh and auth writes with a lock keyed to the canonical token path;
retain the lock for the command to prevent stale records overwriting newer ones.
Acquire the profile/run lock before the token lock in all commands.

Use a narrow Go Drive adapter with injectable HTTP transport. It exposes metadata,
paginated child listing, media download, fixture creation, and owned-object trash.
Restrict the first slice to My Drive; shared-drive support is deferred. Listing
must consume every page, including empty pages with continuation tokens, and
reject incomplete searches and malformed or repeated continuation tokens.
Read-only requests may retry transient failures with bounded backoff and
cancellation; create requests require reconciliation rather than blind retries.

## Shared planning and execution

Keep the shared engine below the CLI: remote scanning, local validation, a pure
deterministic planner, and an executor. Execute sequentially initially; concurrency
adds little value for the baseline and complicates recovery.

Before changing the destination, obtain a complete supported remote tree and
validate all mapped paths. Preserve names exactly; reject empty names, `.` and
`..`, slash, backslash, NUL, invalid UTF-8, duplicate siblings, and paths exceeding
the destination filesystem's limits. Reject Google-native entries other than
folders, shortcuts, files not owned by the authenticated account, shared-drive
entries, and unavailable downloads. Account-owned shared files are allowed;
third-party-owned shared content is deferred. Require size and MD5 checksum
metadata for ordinary files in this first slice; missing metadata fails preflight.
Detect local filesystem name collisions rather than claiming general macOS or
Windows name portability. Linux is the acceptance target.

Reject destination or descendant symlinks and non-regular local entries. Anchor
filesystem operations beneath the opened destination using Go's root-confined
filesystem APIs and reject symlinks explicitly; do not rely only on path-prefix
checks. Reject overlapping or nested registered destinations and use a
destination lock in addition to the profile lock so separate profiles cannot
write the same tree. Locks are nonblocking and OS-backed; fail clearly if held.

The planner produces directory creation, file download, and skip operations in
stable parent-first order. Persist versioned profile state outside the downloaded
tree: binding, remote IDs and fingerprints, local SHA-256 hashes, and completed
operations. Rehash existing files before skipping; timestamps alone are not proof.

This is initial population with resumability, not incremental sync: additions can
complete an interrupted population, but modification, removal, or rename of a
previously completed remote entry reports an unsupported-change error. Local
modifications and unexpected entries also fail without overwriting or deleting
anything. Already completed unchanged files remain intact.

## Publication and failure recovery

Download each file into an exclusively created temporary file in its destination
directory. Validate byte count and the Drive binary checksum, then sync and close
it before publishing without replacing an existing destination. Recheck metadata
after download and reject a changed remote version. Keep ownership of temporary
paths in durable pending-operation state so recovery never removes arbitrary
files based only on a filename pattern.

Persist an intent containing the expected remote identity before downloading,
then persist the verified content hash before publication. After atomic
publication, mark completion atomically. On restart, a pending operation whose
final file matches the durable verified hash is adopted; mismatches fail. An
incomplete temporary download restarts from zero. Directory intents similarly
allow recovery from creation before completion was recorded.

Cancellation, checksum mismatch, disk errors, interrupted listings, and state
write failures return failure and preserve completed files and diagnostic state.
Never mark a failed scan or transfer complete. No deletion, overwrite, byte-range
resume, or account-wide snapshot guarantee is provided in this slice. Per-file
version checks detect changes during transfers; Drive traversal is not a
transactional snapshot of a concurrently edited tree.

## Testing and live execution

Default offline tests require no credentials. Cover manifest validation, exact
membership verification, pagination, incomplete listings, unsafe paths, unsupported
entries, deterministic plans, no-op reruns, local conflicts, token refresh
persistence, locking, checksum mismatch, cancellation, and failure injection at
state/publication boundaries. Assert zero writes on incomplete preflight scans.
Exercise the real HTTP adapter against a fake Drive server, not only interface
mocks. Test profile and destination exclusion across processes on Linux.

Add a `mise run test-live` task requiring explicit `GROVE_LIVE_TEST=1`, an absolute
`GROVE_TEST_TOKEN_FILE`, and an absolute `GROVE_TEST_RUNS_DIR` outside the repo.
Run only the dedicated live test package; default `mise run test` skips it before
loading credentials. Public CI runs offline checks and receives no live secrets.

Each live run creates a private local run directory, seeds a new remote root,
inspects it, syncs into a new local directory, independently verifies content,
then syncs again and asserts zero media download requests and zero file writes.
Metadata reads and local hash reads are allowed on the second run. A successful
run cleans up owned remote objects; a failed run retains its record for explicit
inspection and cleanup. Interrupted-run recovery is primarily deterministic
offline testing, supplemented by an opt-in live cancellation case.

The user authenticates locally with the existing `grove auth --access read-write
--token-file ...` flow using the dedicated account. No token is pasted into chat.
Run `mise run fmt`, `mise run lint`, `mise run test`, and `mise run test-race` for
implementation, plus native and Linux builds. Report live validation as pending
until actually executed with user-enabled credentials.

## Completion and handoff

The branch is complete when fixture operations and initial sync meet the above
offline checks, and live seed → inspect → sync → verify → no-op repeat → cleanup
is demonstrated or explicitly recorded as awaiting local authentication.
Update product, architecture, testbed, and status documents to reflect actual
implemented behavior. No daemon, uploads through sync, ongoing remote-change
reconciliation, shared content, or advanced name mapping is included.

After written-spec approval, create the paired plan at
`docs/implementation-plans/000002-initial-sync-plan.md`. Implementation starts
after that plan is reviewed and its execution method selected.

## API references

- [Drive v3 file listing](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/list)
- [Drive search and app properties](https://developers.google.com/workspace/drive/api/guides/search-files)
- [Download ordinary files and export native documents](https://developers.google.com/workspace/drive/api/guides/manage-downloads)
