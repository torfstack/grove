# Grove Authentication Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans for inline execution,
> or superpowers:subagent-driven-development if the user selects delegation.
> Complete tasks in order; checkbox steps track progress.

**Goal:** Deliver a tested `grove auth` command that authorizes Drive through the
browser and persists access and refresh tokens.

**Architecture:** Cobra commands call an injected authentication service. The
service combines Desktop client loading, a loopback OAuth flow, browser opening,
and atomic token storage. OAuth and filesystem tests run offline.

**Tech Stack:** Go 1.27.2, github.com/spf13/cobra, golang.org/x/oauth2,
and the Go standard library.

**Spec:** [000001-auth-design.md](../spec/000001-auth-design.md)

Status: approved and implemented; manual live Google consent remains pending.
Dependencies are pinned in go.mod/go.sum. Libraries belong in
go.mod; development executables belong in mise.toml. No CLI generator is needed.
Use `module grove` initially, since no hosting path is known; rename it when the
repository hosting location is established.

## Global constraints

- Linux first; macOS supports development and the browser auth smoke check.
- Required `--client-secret`; `--access` defaults to `read-only`, accepts only
  `read-only` or `read-write`; `--token-file` overrides the default location.
- Read-only scope: `https://www.googleapis.com/auth/drive.readonly`.
- Read-write scope: `https://www.googleapis.com/auth/drive`.
- Loopback listener: 127.0.0.1, random port, GET `/callback`, five-minute deadline.
- Random state, S256 PKCE, offline access, consent, and account selection.
- New private directories 0700; token files 0600; atomic replacement on Unix.
- No tokens, authorization codes, client secrets, or raw provider bodies in output.
- Failed authentication preserves the prior token. No auth locking; the last
  successful atomic write wins if simultaneous logins target the same file.
- No OpenID scopes, Drive commands, daemon, profiles, keychain, or refresh-token
  source implementation in this slice. Windows auth is explicitly unsupported.

## Review focus

1. Pasted or provider-controlled error text must not leak secrets (tasks 2 and 4).
2. Stray browser requests and wrong-state callbacks must not abort login (task 3).
3. Ctrl-C while token exchange stalls must close the listener (task 4).
4. Relative credential paths and Linux XDG configuration must resolve predictably
   without reading the real home directory in tests (tasks 1 and 2).
5. Replacing an existing token must retain owner-only permissions even if the
   previous token file was more permissive (task 2).

## Files and interfaces

- `cmd/grove/main.go`: signal-aware entry point and one error print.
- `internal/cli/root.go`, `auth.go`, `auth_test.go`: command tree and flag tests.
- `internal/auth/client.go`, `client_test.go`: validate Desktop JSON and scopes.
- `internal/auth/store.go`, `store_test.go`: record format and atomic persistence.
- `internal/auth/flow.go`, `flow_test.go`: authorization URL and callback lifecycle.
- `internal/auth/service.go`, `service_test.go`: flow and store orchestration.
- `internal/browser/open.go`, `open_test.go`: shell-free OS launch adapter.
- `go.mod`, `go.sum`, `.gitignore`: dependencies and local artifact exclusions.
- `README.md`, `docs/STATUS.md`, `docs/ARCHITECTURE.md`, `mise.toml`: usage and checks.

Shared interfaces (define them in the listed owning files):

```go
// internal/auth/client.go
type Client struct { ID, Secret, Path string }
func LoadClient(path string) (Client, error)
func Scope(access string) (string, error)

// internal/auth/store.go; JSON keys use snake_case
type Record struct {
    Version int
    ClientID string
    ClientSecretPath string
    Scopes []string
    Token *oauth2.Token
}
func DefaultTokenPath() (string, error)
func SaveRecord(path string, record Record) error
func LoadRecord(path string) (Record, error)

// internal/auth/flow.go
type OpenBrowser func(context.Context, string) error
type Flow struct {
    Endpoint oauth2.Endpoint
    HTTPClient *http.Client
    OpenBrowser OpenBrowser
    Output io.Writer
}
func (f Flow) Authorize(ctx context.Context, client Client, scope string) (*oauth2.Token, error)

// internal/auth/service.go
type Options struct { ClientSecret, Access, TokenFile string }
type Service struct { Flow Flow }
func (s Service) Authenticate(ctx context.Context, opts Options) (string, error)

// internal/browser/open.go
func Open(ctx context.Context, url string) error

// internal/cli/root.go
type AuthenticateFunc func(context.Context, auth.Options) (string, error)
func NewRoot(authenticate AuthenticateFunc) *cobra.Command
```

## Task 1: Testable CLI and validated client input

**Files:** go.mod/go.sum, .gitignore, internal/cli/*, internal/auth/client*,
cmd/grove/main.go.

**Consumes:** approved command and scope requirements.
**Produces:** Client, Options (initially declared in service.go), Scope,
LoadClient, AuthenticateFunc, NewRoot; main can use an explicit unavailable
service stub until task 4 wires the real service.

- [x] Initialize module and pin dependencies. Ignore `/bin/`, `/grove`,
  `/groved`, `/mise.local.toml`, `/google_client_secret.json`, `/token.json`.
- [x] Write `TestAuthFlags`: inject a recorder; assert missing client-secret,
  invalid access, unknown flags, and positional arguments fail without calling
  it; valid args pass exact options; help performs no authentication.
- [x] Write `TestLoadClient` and `TestScope`: accept installed Desktop JSON,
  require nonempty ID/secret, reject web-client JSON and malformed JSON, resolve
  a relative path to absolute, and assert exact scope strings. Errors must not
  contain a sentinel secret embedded in malformed input.
- [x] Run `mise exec -- go test ./internal/cli ./internal/auth`; confirm failures
  caused by missing behavior, then implement the interfaces and rerun to pass.
- [x] Configure Cobra RunE, injected I/O, no positional args, and centralized
  errors (SilenceErrors/SilenceUsage). Main uses signal.NotifyContext for SIGINT
  and exits nonzero on error. Commit the task's specific files after checks pass.

## Task 2: Private atomic token storage

**Files:** internal/auth/store*.
**Consumes:** Client and oauth2.Token.
**Produces:** Record, DefaultTokenPath, SaveRecord, LoadRecord.

- [x] Write `TestRecordRoundTrip`: version 1, exact client metadata/scopes/token,
  0600 file and 0700 newly created directory. Set HOME/XDG_CONFIG_HOME to test
  directories; verify Linux XDG and OS config defaults without touching user data.
- [x] Write `TestAtomicSaveFailurePreservesToken`, `TestRejectSymlinkDestination`,
  `TestLoadUnsupportedVersion`, and `TestRecordErrorsRedacted`. Use controlled
  filesystem faults and assert the previous valid record remains byte-identical.
- [x] Write `TestReplacementPermissions`: replace an existing 0644 token file
  and assert the complete new record has 0600 permissions.
- [x] Run `mise exec -- go test ./internal/auth`; confirm new tests fail, then
  implement same-directory temporary write, sync, close, rename, and cleanup.
  Reject missing access/refresh tokens and symlink destinations. Do not change
  permissions on unrelated preexisting parent directories.
- [x] Rerun storage tests; commit the task's files after checks pass.

## Task 3: Loopback OAuth and browser adapter

**Files:** internal/auth/flow*, internal/browser/open*.
**Consumes:** Client and scope string.
**Produces:** Flow.Authorize and browser.Open.

- [x] Write a fake-provider test that parses the generated authorization URL,
  checks scope, `access_type=offline`, `prompt=consent select_account`, random
  state, and `code_challenge_method=S256`. Its token endpoint verifies the
  challenge against the submitted verifier, code, and redirect URI before
  returning a synthetic access/refresh token. Assert the returned token matches.
- [x] Write callback tests for wrong state followed by valid state, unrelated
  path, non-GET, missing/duplicate state/code/error, code plus error, denied
  consent, and simultaneous callbacks. Assert one exchange at most; wrong-state
  requests leave the legitimate login pending. Set `Referrer-Policy: no-referrer`
  and return static browser text with no reflected query values.
- [x] Write cancellation/short-deadline tests: no callback and stalled exchange
  both return promptly and close the listener. Tests inject deadlines rather
  than waiting five minutes. Provider errors must not print sentinel secrets.
- [x] Write browser adapter tests using an injected process runner: Linux uses
  `xdg-open <url>`, macOS uses `open <url>`, each as argv without a shell; unsupported
  platforms return a clear error. Wait/reap helper processes with context timeout
  so browser launch cannot prevent callback processing indefinitely.
- [x] Run `mise exec -- go test ./internal/auth ./internal/browser`, observe
  failures, then implement using oauth2.GenerateVerifier, S256ChallengeOption,
  VerifierOption and cryptographic state. Production endpoints come from
  oauth2/google.Endpoint; fake endpoints are injected only through test wiring.
- [x] Print the authorization URL once for manual opening. Browser-launch failure
  leaves authorization pending until callback, deadline, or cancellation.
- [x] Run `mise exec -- go test -race ./internal/auth ./internal/browser`; confirm
  pass and commit the task's files.

## Task 4: Wire the authentication service end to end

**Files:** internal/auth/service*, cmd/grove/main.go, internal/cli/auth_test.go.
**Consumes:** Task 1 CLI/client, task 2 storage, task 3 flow/browser.
**Produces:** Service.Authenticate and a usable grove auth executable.

- [x] Write `TestAuthenticatePersistsRecord` with a temporary credential file,
  fake provider, injected browser, and isolated token path. Assert record version,
  ID, absolute credential path, scope, and token; stdout contains the saved path
  and never token/code/secret sentinel values. Read the file independently.
- [x] Write service tests for an existing token plus denial, cancellation,
  missing refresh token, exchange failure, and save failure. Assert prior bytes
  remain unchanged.
- [x] Run `mise exec -- go test ./...`, confirm failures, then implement validation,
  default-path resolution, five-minute
  context, token validation, record construction, and save. Pass errors through
  sanitized categories; never expose raw oauth2.RetrieveError bodies.
- [x] Wire main to production Google endpoints, browser.Open, command output, and
  Service.Authenticate. Success output is `Authentication saved to <path>` only
  after persistence succeeds. Root help lists implemented commands only.
- [x] Run `mise exec -- go test -race ./...`; commit after passing checks.

## Task 5: Verification, developer tasks, and live handoff

**Files:** mise.toml, README.md, docs/STATUS.md, docs/ARCHITECTURE.md.
**Consumes:** working auth command.
**Produces:** documented usage and verified implementation evidence.

- [x] Add mise tasks `fmt`, `vet`, `test`, `test-race`, and `build` using
  `gofmt -w cmd internal`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
  and `go build -o bin/grove ./cmd/grove`. No additional executables are required.
- [x] Run format and verify `gofmt -l cmd internal` has no output. Run vet, tests,
  race tests, native build, and Linux cross-build with
  `GOOS=linux GOARCH=amd64 go build -o bin/grove-linux-amd64 ./cmd/grove` through mise.
  Cross-build is compilation evidence, not a Linux runtime test.
- [x] Run binary help and auth help. Document read-only usage, explicit read-write
  usage, token location/override, manual URL fallback, five-minute timeout,
  plaintext token storage, and seven-day Testing-mode reauthentication.
- [ ] Offer the live browser check with the dedicated account when the user is
  present. It writes outside the repo and opens a GUI, so use required execution
  approval. Do not inspect or print token contents; confirm persistence by metadata
  and validated loading. Record whether live consent was actually completed.
- [x] Update status/architecture with final behavior, checks, and remaining limits;
  commit the task's specific files. Do not claim Drive API access was verified
  merely because OAuth succeeded; that belongs to the next Drive testbed slice.

## Execution record

Executed inline on `feat/auth` in the existing directory, as authorized by the
user. Tasks 1–4 and the offline verification in task 5 are complete. The manual
live check remains pending; no actual Google tokens have been requested.

Cobra v1.10.2 and oauth2 v0.37.0 are pinned. No additional development executables
were needed. The initial repository had no commits, so changes were collected
for one initial implementation commit rather than per-task commits.

Storage tests inject a rename failure through a private helper to exercise
preservation and cleanup deterministically. A fresh reviewer found premature
callback connection shutdown. An independent callback-client regression test
failed with EOF under race detection; bounded graceful shutdown fixed it.

Review boundaries: Windows, persistent refresh, Drive calls and locking remain
deferred as specified. Live browser consent and Linux runtime tests remain
pending. Atomic replacement is implemented; power-loss durability of the
directory entry is not promised. Sparse-comment style is recorded in AGENTS.md.
