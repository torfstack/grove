# Architecture

Authentication, the Drive fixture lifecycle, and initial/incremental download sync
are implemented. Explicit local uploads, revision-aware conflict resolution, and
safe local deletion propagation are implemented in the shared sync engine. The
approved deletion contract is in
[spec 000007](spec/000007-deletion-propagation-design.md). Linux periodic daemon
orchestration is implemented under [spec 000009](spec/000009-linux-daemon-design.md).

[Spec 000004](spec/000004-local-upload-design.md) records the upload and
conflict-recovery contract. The engine uses a guarded streaming Drive-write
boundary, private immutable staging, version-3 upload journals, revision
preservation, profile-scoped conflict state, and service methods consumed by the
CLI and daemon; a future UI can use the same boundary. Drive does not provide conditional writes; see the spec
for revision-retention and quota limits.

## Current authentication implementation

`cmd/grove` wires Cobra commands in `internal/cli` to `internal/auth`. The auth
service loads Desktop credentials, requests Drive authorization using
golang.org/x/oauth2, and writes a versioned token record atomically. Production
OAuth endpoints come from the library's Google configuration, not client JSON.

`internal/browser` opens the system browser using argument-based process execution
on Linux and macOS. Auth receives a GET callback on a temporary IPv4 loopback
listener, validates random state and S256 PKCE, and shuts down gracefully after
the response. Its five-minute deadline and parent context support cancellation.

Credentials remain outside the repo. Tokens are plaintext 0600 files under the
OS user config directory or an explicit path; newly created directories are 0700.
Auth and API sessions serialize canonical-token-path access with OS locks.
Successful refreshes are persisted atomically, preserving an omitted refresh
token; persistence failures fail the request. Windows auth remains deferred.

## Binaries and shared engine

- `grove`: one-shot commands, including interactive authentication.
- `groved`: foreground periodic sync of one existing Linux profile, sequential
  attempts, cancellation, and graceful shutdown. A distributed systemd user
  template supplies optional Linux background lifecycle; opt-in local watching
  requests debounced sequential attempts while retaining periodic scans.
- `grove-tray`: Linux graphical-session companion; offline profile observations,
  StatusNotifierItem/D-BusMenu, and optional validated user-service controls.
  Its lifetime is independent of the daemon.
- Shared Go packages: Drive access, local scanning, persisted sync state,
  deterministic planning, and operation execution.

The daemon calls shared packages directly rather than repeatedly spawning the
CLI. Both binaries use the same engine.

The planner takes local, remote, and previously recorded state and
produces operations. Keep network access and filesystem mutations in execution
so planning can be tested independently.

## Concurrency and OS integration

Only one writer may operate on a sync profile at a time. A per-profile lock is
the initial proposed mechanism. Local IPC may later let the CLI trigger work
through a running daemon.

Linux background integration uses the optional
[systemd user-service template](../contrib/systemd/README.md), one instance per
daemon config. The unit launches the foreground binary directly, leaves fatal
exits stopped for operator inspection, and waits without a forced shutdown
timeout. It does not change engine scheduling, exclusion, or recovery.
Other platforms will supply their own process lifecycle integration.

## Initial sync and fixture implementation

`internal/drive` implements a narrow Drive v3 HTTP adapter with explicit metadata,
complete pagination, sanitized errors, bounded read retries, and no blind create
retries. `internal/fixture` validates a versioned manifest and independently
checks downloaded membership and SHA-256 content. Seed journals create intents
and confirmed IDs; inspect reconciles uncertain outcomes through random ownership
properties; cleanup validates all descendants before child-first trash.

`internal/syncengine` scans a complete supported remote tree, scans/hash-checks
local files, plans deterministic operations, and executes sequentially under a
profile and canonical destination lease. Root-confined operations reject symlinks.
A private journal tracks download intents, exclusive temporary paths, verified
hashes, and completion. Unix hard-link publication cannot replace a destination;
verified final files can be adopted after interrupted completion recording.
Files use actual-byte size/MD5 checks and a post-transfer metadata comparison
before publication. The download-specific comparison accepts a numeric version
increase only with matching nonempty revision/time evidence and unchanged file
structure, ownership/location, active status, and capability. Equal versions
remain required when revision/time evidence is absent. Pending initial files
and unpublished replacements use the same comparison during recovery; initial
recovery also verifies the saved relative path. Planned journal snapshots stay
unchanged, and later scans reconcile newer versions through metadata-record
operations. Move, upload, and Trash checks retain their separate rules. See
[spec 000008](spec/000008-download-metadata-version-design.md).

`internal/privatefs` supplies atomic owner-only JSON records and nonblocking
process locks. A private destination registry prevents overlapping profile
bindings. Directory-inode leases, shared ancestor locks, and a sibling lock file
exclude aliases and nested writers across registry locations. Destination parents
must already exist; creating an unleased parent tree is not supported. `cmd/grove/services.go` opens authentication after the profile/run locks;
CLI commands receive injected services. The engine's injectable `Service` supports
HTTP integration tests and the future daemon without spawning the CLI.

State and fixtures remain outside the downloaded tree. Spec 000003 extends the
same engine with remote additions, replacements, and moves. Spec 000004 adds
two-way uploads and durable conflict choices. Remote removals stop preflight, and
this is not a transactionally consistent remote snapshot. Spec 000007 implements
baseline-verified local deletion propagation through journaled Drive Trash
operations. Baseline, incremental,
and two-way upload/race-recovery live acceptance passed on macOS on 2026-10-10.
Full Linux live acceptance passed on 2026-10-22 after spec 000008 implemented
metadata-version tolerance for downloads and recovery.

## Incremental reconciliation and recovery

The planner matches stable remote IDs against the saved baseline, validates local
membership/content, and resolves structural dependencies in deterministic order.
It simulates paths after ancestor moves, so carried descendants are not moved
again. It emits typed mkdir/download/replace/move/record/skip operations; same-byte
version changes use record operations without transfer.

Execution separates transfer verification, baseline/journal persistence,
replacement transitions, and subtree moves. Version-3 state retains the original
initial-download pending shape and adds a mutually exclusive typed transaction
for replacements, moves, uploads, and Trash. Version-1 recovery precedes atomic migration. Journal
validation rejects unsupported phases, unsafe artifacts, inconsistent baselines,
and unrelated identities before recovery.

Replacement stages verified bytes, moves old content to an owned backup without
replacement, publishes into the absent target, commits its baseline with cleanup
intent still present, then removes hash-verified artifacts and clears intent.
Moves validate exact source membership/hashes, use one native no-replace rename,
sync both parents, and durably remap the subtree. Linux renameat2 and macOS
renameatx_np use parent descriptors opened through the rooted filesystem boundary;
x/sys is pinned in go.mod. Unsupported primitives fail preflight. Recovery checks
observed bytes/membership instead of trusting the last recorded phase alone.

Fixture mutation uses a separate Drive MutationAPI; the sync engine keeps its
existing read-only API contract. ApplyChanges accepts loader-validated payloads,
checks owned complete membership, journals each create/update, and reconciles
uncertain outcomes through owned identity and expected content. It adds no new
CLI mutation command or second fixture lifecycle.

## Two-way uploads and conflict resolution

Two-way sync requires a populated profile, explicit `--two-way`, and read-write
Drive authorization. The same deterministic planner combines remote downloads
with local create/update operations. Existing-file uploads require the saved
content revision and path to match; a metadata-version-only increase is allowed
when head revision, modified time, size, and MD5 remain unchanged. Local content
is staged privately and journaled before mutation. Upload recovery reconciles an
uncertain result without retrying the remote write.

The engine pins the observed head before updating and scans revision history
afterward. Detected competitors are pinned, independently downloaded and
verified, and durably captured as cloud candidates alongside the local candidate
under [spec 000006](spec/000006-multiple-conflict-candidates-design.md). The
profile service and CLI expose stable ordinals for explicit cloud selection;
automatic policies do not choose among multiple preserved cloud candidates. Conflict policy and resolution stay in the shared service
boundary so a future desktop daemon can expose them without a second sync engine.
Upload journals record the original keepForever state. Pre-upload recovery
releases only a pin Grove introduced; missing ownership metadata preserves it.
Drive does not permit unpinning non-head keepForever revisions, so successful
uploads consume the per-file 200-revision pin quota. Incomplete capture remains
journaled and pinned; a complete verified candidate set becomes a durable
conflict available for deliberate resolution.

Cloud-side deletion conflict resolution drops the selected tombstone subtree
baseline and restores the reviewed remote tree through verified folder creation
and downloads. The profile stays marked incomplete until restoration finishes,
so an interrupted restore can resume through download-only sync without
interpreting a missing child as a new deletion.

## Local deletion propagation

The two-way planner treats absent tracked paths as tombstones after complete
local and remote scans. Files are matched by identity, parent, path, revision,
modified time, size, and MD5. Missing folders collapse to one operation only
when the listed live remote subtree exactly matches the tracked baseline.
Changed or expanded subtrees become durable conflicts and ignore configured
automatic conflict policies.

Trash operations store the tracked baseline subtree and a live remote fingerprint
in the existing profile journal. The executor performs a final complete scan and
identity read before issuing one Drive Trash request. Recovery never repeats an
uncertain request: it checks `explicitlyTrashed` on the root and inherited
`trashed` on each recorded descendant before committing baseline removal. An
explicit cloud conflict choice removes the old tracked subtree atomically and
restores the current remote tree through verified folder creation and downloads.
The Drive API has no conditional Trash operation, so an external remote change
between preflight and the request remains a narrow race.

## Operator reconciliation

The recovery service uses the same validated upload journal and executor as
normal sync, without invoking the planner or whole-tree scan. Offline inspection
returns structured reasons and stage availability; retry holds profile,
destination and token exclusion and processes only the original transaction.
Recovered outcomes validate structure and independently downloaded hashes around
stable revision checks. Fresh anchored history can resolve a transient listing
failure, but recorded competitor obligations are retained. Pre-send retirement
persists a `retired` journal phase until identity/hash-checked cleanup finishes.
Selected conflict uploads retain their original conflict evidence in the
transaction. Baseline/candidate commit atomically retires the original conflict
record; its stages remain journal-owned cleanup obligations until removed safely.
Conflict capture rechecks the recorded head before committing, and cleanup
restarts report any surviving conflict belonging to the original transaction.

Offline export opens a recorded stage through the profile root, streams verified
bytes into a private temporary file, and publishes by no-replace hard link into
an existing output parent. Directory leases and the destination registry guard
coordinate exclusion during publication. Export checks source and parent
identities, retains the original stage and state, and never authenticates.
CLI formatting uses only sanitized reports and safe error categories. See
[spec 000005](spec/000005-operator-reconciliation-design.md) for the contract and
indefinite-blocking limits.

## Linux periodic daemon

`cmd/groved` supplies signals and argument validation to `internal/daemon`.
The scheduler directly calls the shared engine's locked existing-profile API;
it never shells out to sync or creates a missing profile. Startup validates a
strict private config and retains its binding, acquires a lifetime `daemon.lock`,
and inspects the state under the per-attempt profile lock. Each attempt reloads
state and compares the original binding before authentication. Attempts run
sequentially with elapsed waits after completion. Existing profile, destination,
and token locks coordinate all writers, including manual CLI commands.

SIGINT/SIGTERM cancel the active context and await its return and cleanup before
releasing lifetime ownership. Private Drive error wrappers retain cancellation
causes without putting request URLs or response bodies in displayed errors.
Shared engine and OAuth refresh boundaries preserve those causes as well, while
joining genuine cleanup failures so cancellation cannot hide them.
`daemon-status.json` is a private atomic advisory record, written only in an
existing profile. Record failure stops scheduling. Offline `grove status` uses
a non-mutating shared lock probe for ownership, maps history into a public DTO,
and independently locks the profile for its summary. Neither history nor a PID
is liveness evidence or authority to mutate data. See
[spec 000009](spec/000009-linux-daemon-design.md).

## Linux local watching

[Spec 000011](spec/000011-local-watching-design.md) adds a Linux inotify adapter
behind `Watcher`, using pinned x/sys and `/proc/self/fd` to register anchored
no-follow directory descriptors. A bounded mailbox preserves first/latest hint
times and sticky failures; its reader drains concurrently with serialized
coverage walks. Structural events and overflow request recursive repair, not
path-based engine operations. Root identity loss or failed repair stops visibly.

The scheduler keeps independent periodic, debounce, and retry deadlines. A single
engine worker retains original binding and mode checks while the scheduler
consumes hints during work. One-second quiet/five-second maximum debounce does
not prove write completion. Failed/busy attempts restrict event retries for one
interval. Watcher-generated hints from Grove's writes may cause a no-op follow-up;
read/hash scans do not trigger events. Historical status exposes configured
watching and earliest advisory deadline, not current watch health. Shutdown joins
engine and native reader cleanup before releasing lifetime ownership. Periodic-
only execution retains its original completion-based wait path.

## Remote change hints

[Spec 000012](spec/000012-remote-change-detection-design.md) adds a separate
Drive ChangesAPI and locked Service.CheckRemoteChanges boundary. It validates
the original existing-profile binding before opening read-only authentication,
traverses bounded pages, and closes API/profile leases before return. Results
carry only an opaque in-memory cursor and dirty bit. Partial/error pagination
retains the original cursor; genuine session/profile cleanup failures preserve
joined causes with a typed fatal marker and sanitized display.

An optional remote source emits bounded dirty hints and latest historical check
observations into the existing coordinator. A context-aware in-process gate
serializes each bounded check with RunExisting, through cleanup. Polling waits
its configured interval after each result and includes gate wait in its ten-
second budget. The shared scheduler retains debounce, sync retry restrictions
and independent periodic deadlines; remote checks never reset sync counts or
become operation plans. Both sources disabled retains the old periodic-only path.

The coordinator alone persists remote history, across valid phase transitions.
It cancels work, joins workers/sources, drains sticky faults and then releases
lifetime ownership. Native watcher semantics remain intact. Ephemeral cursor
initialization emits a dirty hint to close the startup gap, and account-wide
My Drive hints deliberately allow false-positive scans. No event path/ID reaches
the planner, and periodic full scans remain authoritative.


## Linux tray boundary

`cmd/grove-tray` wires the Linux-only `internal/tray` coordinator to the existing
`daemon.Inspect` service. A pure reducer maps observations and bounded last-known
profile facts into icon/tooltip/menu snapshots. One worker serializes offline
inspections; refresh requests coalesce and D-Bus handlers use immutable snapshots.
No profile state decoding, OAuth session, Drive request, or engine mutation is
added to the companion.

A private session-bus connection exports `org.kde.StatusNotifierItem` and
`com.canonical.dbusmenu`, owns one opaque canonical-profile-derived bus name,
and registers with the existing watcher. It re-registers when the watcher
changes. Embedded pixmaps complement named SVG icons. A separate optional
connection observes the user systemd manager, validates the selected unit's
resolved identity/effective ExecStart and private daemon config, then dispatches
explicit start/stop requests and observes their asynchronous jobs. Profile
ownership and manager state remain separate facts. Cancellation joins owned
workers and closes only companion resources; accepted manager jobs and daemon
leases remain outside its ownership.

[Spec 000014](spec/000014-linux-freedesktop-tray-design.md) and its
[plan](implementation-plans/000014-linux-freedesktop-tray-plan.md) record the
contract; [desktop instructions](../contrib/desktop/README.md) cover manual
installation. No full GUI toolkit or daemon IPC is required. Waybar desktop
acceptance is recorded in TESTBED; independent-host compatibility remains unverified.
