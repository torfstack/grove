# Architecture

Authentication is implemented; sync and daemon packages remain to be designed.

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
Auth locking is deferred, so the last successful atomic write wins. Windows auth
and persistent refresh updates are deferred.

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
