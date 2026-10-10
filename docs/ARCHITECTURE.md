# Architecture

Authentication, the Drive fixture lifecycle, and initial download sync are
implemented. Incremental sync and daemon orchestration remain to be designed.

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
- `groved`: background scheduling, local watching, remote polling, retries,
  cancellation, and graceful shutdown.
- Shared Go packages: Drive access, local scanning, persisted sync state,
  deterministic planning, and operation execution.

The daemon calls shared packages directly rather than repeatedly spawning the
CLI. Start with the CLI, then add the daemon.

The proposed planner takes local, remote, and previously recorded state and
produces operations. Keep network access and filesystem mutations in execution
so planning can be tested independently.

## Concurrency and OS integration

Only one writer may operate on a sync profile at a time. A per-profile lock is
the initial proposed mechanism. Local IPC may later let the CLI trigger work
through a running daemon.

Linux background integration is planned as a systemd user service. Other
platforms will supply their own process lifecycle integration.

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
Files use size/MD5 checks and a post-transfer version check before publication.

`internal/privatefs` supplies atomic owner-only JSON records and nonblocking
process locks. A private destination registry prevents overlapping profile
bindings. Directory-inode leases, shared ancestor locks, and a sibling lock file
exclude aliases and nested writers across registry locations. Destination parents
must already exist; creating an unleased parent tree is not supported. `cmd/grove/services.go` opens authentication after the profile/run locks;
CLI commands receive injected services. The engine's injectable `Service` supports
HTTP integration tests and the future daemon without spawning the CLI.

State and fixtures remain outside the downloaded tree. Completed populations
reject later remote additions, modifications, renames, and removals; no deletion
or overwrite operations exist. This is not a transactionally consistent remote
snapshot. Live account access remains unverified until the opt-in suite is run.
