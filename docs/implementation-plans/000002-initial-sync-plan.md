# Initial Sync and Fixtures Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:executing-plans for inline execution or
> superpowers:subagent-driven-development if the user selects delegation.
> Implement tasks in order; checkbox steps track progress.

**Goal:** Deliver independently verified initial Drive downloads using fresh,
owned fixtures, with safe restart and no redundant second-run media transfers.

**Architecture:** A small Drive HTTP adapter and persistent OAuth source serve
two separate consumers: fixture tooling and a shared download engine. Fixture
verification uses the committed manifest independently of sync state. Pure
planning stays separate from network calls and rooted filesystem execution.

**Tech Stack:** Go 1.27.2, existing Cobra v1.10.2 and oauth2 v0.37.0, standard
library HTTP/JSON/filesystem APIs; existing pinned mise tools. No new development
executables or Drive SDK dependency are needed.

**Spec:** [000002-initial-sync-design.md](../spec/000002-initial-sync-design.md)

Status: approved for native execution; implementation and offline checks complete,
independent review findings fixed; PR feedback and live acceptance pending.
All work remains on `feat/initial-sync`. This plan is not evidence that code exists.

## Global Constraints

- Linux is the acceptance target; retain macOS development support behind OS boundaries.
- Explicit profile, remote root, local destination, and token paths; immutable profile binding.
- Ordinary account-owned My Drive files/folders only; no native documents, shortcuts,
  duplicate siblings, shared drives, third-party-owned content, sync uploads, or daemon.
- New profile destinations are absent or empty; resumptions use the same profile.
- Preserve names exactly; reject unsafe names, symlinks, collisions, and unsupported entries.
- Complete remote preflight before destination mutations; no deletion or overwrite.
- Private directories 0700, private records 0600, version 1 JSON, atomic record writes.
- Persistent successful refreshes retain an omitted refresh token; save failure is fatal.
- Acquire profile/run lock, destination coordination when needed, then canonical token lock.
- Logs omit credentials, tokens, generated Drive IDs, provider bodies, and synced contents.
- Live tests require `GROVE_LIVE_TEST=1`, absolute `GROVE_TEST_TOKEN_FILE`, and absolute
  `GROVE_TEST_RUNS_DIR` outside Git; no secrets in default tests or public CI.
- Run `mise run fmt`, `mise run lint`, `mise run test`, and `mise run test-race`
  for Go changes. Keep gofmt and the explicit linter list unchanged.

## Review Focus

1. Alternate spellings and symlinked ancestors of paths must not bypass locks (task 1).
2. HTTP retry and redirect behavior must not duplicate creates or leak credentials (task 3).
3. An uncertain root create must remain discoverable without folder-name cleanup (task 5).
4. A case-insensitive destination must reject name collisions before file publication (task 7).
5. Crashes between verified state, publication, and completion must preserve/adopt only
   verified owned files; disk failure must never falsely report success (task 8).

## Files and Shared Contracts

Each implementation file gets its adjacent `_test.go` file. Keep tests in the
owning package; process-based lock tests use a helper subprocess, not timing sleeps.

- `internal/privatefs/record.go`, `lock.go`, `lock_unix.go`, `lock_other.go`:
  canonical paths, private atomic records, OS locks; unsupported OS returns errors.
- `internal/auth/session.go`, `refresh.go`; modify `service.go`: serialized token usage.
- `internal/drive/types.go`, `client.go`, `list.go`, `media.go`, `create.go`, `retry.go`:
  sanitized Drive v3 transport and metadata types.
- `internal/fixture/manifest.go`, `verify.go`, `record.go`, `seed.go`, `inspect.go`,
  `cleanup.go`: manifest oracle and fixture lifecycle, independent of sync packages.
- `internal/syncengine/types.go`, `scan.go`, `plan.go`, `state.go`, `destination.go`,
  `local.go`, `execute.go`, `publish_linux.go`, `publish_darwin.go`, `publish_other.go`,
  `service.go`: pure planning, rooted filesystem, persistence, and orchestration.
- `internal/cli/fixture.go`, `sync.go`; modify `root.go`, auth tests, and
  `cmd/grove/main.go`: dependency injection and command wiring.
- `testdata/fixtures/baseline/manifest.json`, `payloads/*`: deterministic fixture data.
- `tests/live/initial_sync_test.go`, `mise.toml`, README and affected docs: opt-in suite.

Use these exported contracts so tasks agree on names; JSON uses snake_case.
Private helpers and test fault hooks may remain package-local.

```go
// privatefs
func Canonical(path string) (string, error) // absolute; resolve existing ancestors
func WriteJSON(path string, value any) error
func ReadJSON(path string, value any) error
type Lock struct { /* private OS handle */ }
func Acquire(path string) (*Lock, error)
func (l *Lock) Close() error

// auth; OpenSession owns the canonical token lock, caller closes it
type Session struct { HTTPClient *http.Client; /* private lock */ }
func OpenSession(ctx context.Context, tokenPath, access string) (*Session, error)
func (s *Session) Close() error

// drive
type File struct {
    ID, Name, MIMEType, MD5, Version, DriveID string
    Parents []string
    Size int64
    HasSize, OwnedByMe, CanDownload, Trashed bool
    Properties map[string]string
}
type Page struct { Files []File; NextToken string; Incomplete bool }
type Create struct { Name, MIMEType, ParentID string; Properties map[string]string }
type API interface {
    Get(context.Context, string) (File, error)
    List(context.Context, string, string) (Page, error) // query, page token
    Download(context.Context, string, io.Writer) error
    Create(context.Context, Create, io.Reader) (File, error) // nil content = folder
    Trash(context.Context, string) error
}
type ClientOptions struct { BaseURL string; PageSize int }
type Client struct { /* private HTTP client and options */ }
func NewClient(client *http.Client, opts ClientOptions) *Client
func ListAll(ctx context.Context, api API, query string) ([]File, error)

// fixture
type Entry struct { ID, Parent, Name, Kind, Payload, SHA256 string; Size int64 }
type Manifest struct { Version int; Entries []Entry }
type Verified struct { Manifest Manifest; BaseDir string }
type Report struct { Files, Directories int }
type Run struct { Version int; RunID string; Manifest Manifest; Objects []Object }
type Object struct { LogicalID, RemoteID, ParentID, Status string }
func LoadManifest(path string) (Verified, error)
func VerifyLocal(ctx context.Context, v Verified, localDir string) (Report, error)
func Seed(ctx context.Context, api drive.API, v Verified, runDir string) (Run, error)
func Inspect(ctx context.Context, api drive.API, runDir string) (Report, error)
func Cleanup(ctx context.Context, api drive.API, runDir string) error
func LoadRun(runDir string) (Run, error)

// syncengine
type Binding struct { RemoteRoot, LocalDir, TokenFile string }
type Entry struct { Path string; Remote drive.File }
type Snapshot struct { Entries []Entry }
type Completed struct { Path, RemoteID, Version, MD5, SHA256, Kind string; Size int64 }
type Pending struct { Entry Entry; TempPath, VerifiedSHA256, Phase string }
type State struct {
    Version int; Binding Binding; Completed []Completed; Pending *Pending
    PopulationComplete bool
}
type LocalEntry struct { Path, Kind, SHA256 string; Size int64 }
type Operation struct { Kind string; Entry Entry } // mkdir, download, skip
type Plan struct { Operations []Operation }
type Options struct { ProfileDir, RemoteRoot, LocalDir, TokenFile string }
type Result struct { Downloaded, CreatedDirectories, Skipped int }
func Scan(ctx context.Context, api drive.API, rootID string) (Snapshot, error)
func BuildPlan(remote Snapshot, local []LocalEntry, state State) (Plan, error)
func Run(ctx context.Context, opts Options) (Result, error)

// cli; fixture service functions handle run lock before opening auth session
type FixtureOptions struct { Manifest, RunDir, LocalDir, TokenFile string }
type Services struct {
    Authenticate AuthenticateFunc
    Seed func(context.Context, FixtureOptions) (string, error)
    Inspect func(context.Context, FixtureOptions) (fixture.Report, error)
    Verify func(context.Context, FixtureOptions) (fixture.Report, error)
    Cleanup func(context.Context, FixtureOptions) error
    Sync func(context.Context, syncengine.Options) (syncengine.Result, error)
}
func NewRoot(services Services) *cobra.Command
```

## Task 1: Private records and process exclusion

**Files:** create `internal/privatefs/*` and adjacent tests.
**Consumes:** filesystem paths and Go standard library.
**Produces:** `Canonical`, `WriteJSON`, `ReadJSON`, `Acquire`, `Lock.Close` above.

- [x] Write `TestCanonicalAliases`, `TestPrivateRecordRoundTrip`,
  `TestAtomicRecordFailure`, `TestRejectRecordSymlink`, and `TestProcessLock`.
  Assert existing ancestor aliases converge; 0700/0600 modes; failed replacement
  preserves prior bytes; a second process cannot acquire until the first closes.
- [x] Run `mise exec -- go test ./internal/privatefs`; require new tests to fail
  for missing behavior, then implement confined private record writes with
  same-directory temp, sync, close, rename, and parent-directory sync on Unix.
  Reject malformed/unknown record versions in their owning packages.
- [x] Implement nonblocking flock behind Linux/macOS build tags and sanitize errors.
  Preserve lock files rather than unlinking them while a waiter can hold an inode.
- [x] Rerun the package tests with `-race`, run required Go checks, then commit
  `feat: add private state storage and process locks` with this task's files only.

## Task 2: Persistent authenticated sessions

**Files:** create `internal/auth/session.go`, `refresh.go`, adjacent tests;
modify `internal/auth/service.go` and its tests.
**Consumes:** task 1 locks; existing `LoadRecord`, `LoadClient`, `SaveRecord`, `Scope`.
**Produces:** `OpenSession`, `Session.Close`, serialized existing Authenticate.

- [x] Write `TestSessionScopes`, `TestSessionClientMismatch`,
  `TestRefreshPersists`, `TestRefreshRetainsRefreshToken`,
  `TestRefreshSaveFailure`, and `TestAuthSessionExclusion` using fake OAuth endpoints.
  Assert read-only accepts either Drive scope; write requires full Drive scope;
  exactly refreshed token bytes persist; omitted refresh token preserves the old;
  disk failure prevents a successful API request; auth cannot replace a held token.
- [x] Run `mise exec -- go test ./internal/auth`; observe the new failures.
- [x] Implement session loading only after canonical token lock acquisition; verify
  client ID and scopes, use production Google endpoints and injected test endpoints.
  Wrap refresh with serialized successful `SaveRecord`, sanitize RetrieveError,
  and have Authenticate hold the same lock before any token load/write.
- [x] Rerun auth tests with `-race`, required Go checks, and commit
  `feat: persist refreshed tokens under an exclusive session lock`.

## Task 3: Real Drive adapter with offline HTTP coverage

**Files:** create `internal/drive/*` and adjacent tests.
**Consumes:** authenticated `*http.Client`; returns the shared `drive.API` types.
**Produces:** `NewClient`, `ListAll`, and all API methods above.

- [x] Write fake-server tests `TestMetadataFields`, `TestListAllPages`,
  `TestEmptyPageContinues`, `TestIncompleteListing`, `TestRepeatedPageToken`,
  `TestMediaStreams`, `TestCreateMultipart`, `TestTrash`, `TestRetryCancellation`,
  `TestNoCreateRetry`, `TestCrossOriginRedirect`, and `TestErrorsRedacted`.
  Assert explicit size presence, versions/ownership/capabilities; pages all read;
  incomplete/cyclic pagination fails; uploaded binary bytes are exact; ambiguous
  create invokes POST once; cross-origin redirects never forward authorization;
  errors omit ID/body/content sentinel values.
- [x] Run `mise exec -- go test ./internal/drive`; observe new failures.
- [x] Implement Drive v3 `/files`, parent/trashed queries, selected metadata fields,
  multipart creation, `alt=media` download, and trash PATCH. Default base URL is
  Google's Drive v3 URL; alternate URLs are explicit test wiring only.
  Retry read-only requests at most three attempts for 429/5xx with injectable
  delay, bounded Retry-After, and context cancellation; never silently retry POST.
  Reject media redirect origins that could receive authorization.
- [x] Run HTTP tests, required Go checks, and commit `feat: add testable Drive API adapter`.

## Task 4: Deterministic fixture manifest and independent local oracle

**Files:** create `internal/fixture/manifest.go`, `verify.go`, tests, and baseline data.
**Consumes:** task 1 canonical paths and fixture contracts.
**Produces:** `LoadManifest`, `VerifyLocal`, `Verified`, `Manifest`, `Report`.

- [x] Write `TestBaselineManifest`, `TestManifestInvalidGraph`,
  `TestManifestUnsafePayload`, `TestManifestHashes`, and `TestVerifyExactTree`.
  Assert root uniqueness, valid folder parents, no cycles or duplicate IDs/names;
  reject symlink/escaping payloads; reject wrong sizes/hashes; detect unexpected
  files, missing empty folders, zero-byte mismatches, and non-regular entries.
- [x] Run `mise exec -- go test ./internal/fixture`; observe new failures.
- [x] Implement strict version-1 manifest decoding and exact safe-name validation.
  Include deterministic nested text, Unicode, empty folders, zero-byte payload,
  binary payload, and multiple sibling files; commit real expected SHA-256 values.
  Local verification reads payload expectations and actual file bytes independently
  of any sync engine state, rejects symlinks, and honors cancellation.
- [x] Rerun tests, required Go checks, and commit `feat: add fixture manifest and independent verification`.

## Task 5: Owned fixture seeding, inspection, and cleanup

**Files:** create `internal/fixture/record.go`, `seed.go`, `inspect.go`, `cleanup.go`, tests.
**Consumes:** tasks 1, 3, 4; shared `drive.API` and `Verified`.
**Produces:** `Seed`, `Inspect`, `Cleanup`, `LoadRun`, `Run`, `Object`.

- [x] Write `TestSeedFreshRun`, `TestSeedPersistsBeforeNextCreate`,
  `TestAmbiguousRootReconciliation`, `TestAmbiguousChildReconciliation`,
  `TestInspectRemoteHashes`, `TestCleanupOwnedOnly`, `TestCleanupIncompleteScan`,
  `TestCleanupUnknownDescendant`, and `TestCleanupResumes` against the fake HTTP API.
  Assert pending intent saved before create; successful IDs saved before next POST;
  uncertain root found by random properties, no folder-name query; multiple matches
  fail; inspection downloads and checks exact expected bytes; cleanup aborts before
  trash on failed listing/unknown descendants; already missing/trashed IDs are safe.
- [x] Run `mise exec -- go test ./internal/fixture`; observe new failures.
- [x] Implement random run ownership using `grove_run`/`grove_entry` app properties;
  objects transition pending → created → trashed in `run.json`. Store the validated
  manifest copy. Seed uses topological order, requires a fresh run directory,
  and never resumes creates implicitly after uncertainty. All run operations
  require their caller to hold `run.lock`; the production CLI services in task 9
  acquire it before opening the token session. Inspection reconciles and persists
  pending IDs first. Document that lock contract on the lifecycle entry points.
- [x] Implement complete ownership/parent/membership preflight and child-first trash.
  Check ownership again before each PATCH. Stop on permission errors or ambiguous
  objects; never trash a root containing unknown descendants. Retain failed records.
- [x] Rerun tests, required Go checks, and commit `feat: add owned Drive fixture lifecycle`.

## Task 6: Complete remote scanning and deterministic planning

**Files:** create `internal/syncengine/types.go`, `scan.go`, `plan.go`, adjacent tests.
**Consumes:** `drive.API`; fixture code remains independent.
**Produces:** `Snapshot`, `State` types, `Scan`, `BuildPlan`, operation contracts.

- [x] Write `TestScanSupportedTree`, `TestScanIncompleteNoPlan`,
  `TestScanRejectsUnsupported`, `TestScanUnsafeNames`, `TestPlanStableOrder`,
  `TestPlanResume`, `TestPlanNoOp`, and `TestPlanConflicts`.
  Assert every page consumed; root must be an owned My Drive folder; reject
  unsafe names, duplicate siblings, non-owned files, shortcuts, absent checksums,
  unavailable downloads, and malformed/cyclic trees. Shuffle input and require
  identical plans; unchanged existing hashes yield skip; changed/removed completed
  remote IDs or changed/unexpected local files yield errors, never overwrite/delete.
- [x] Run `mise exec -- go test ./internal/syncengine`; observe new failures.
- [x] Implement complete recursive preflight and pure stable parent-first planning.
  Fingerprints use identity, path, version, size, and MD5; completed directories
  also preserve identity/path. Support additions only while resuming interrupted
  initial population; freeze completed populations against later additions too.
- [x] Rerun tests, required Go checks, and commit `feat: plan initial downloads from complete snapshots`.

## Task 7: Rooted destinations, profile state, and collision exclusion

**Files:** create `internal/syncengine/state.go`, `destination.go`, `local.go`, tests.
**Consumes:** task 1 locks/records and task 6 `State`/`Binding`/`LocalEntry`.
**Produces:** private state load/save, local scan, destination lease used by task 8.

- [x] Write `TestImmutableBinding`, `TestNewDestinationEmpty`,
  `TestRejectStateInsideDestination`, `TestDestinationAliases`,
  `TestNestedDestinationExclusion`, `TestLocalSymlinkRejected`,
  `TestFilesystemNameCollision`, and `TestLocalHashesDetectEdits`.
  Assert profile/run/token data outside the destination; absent destination accepted;
  preexisting content rejected; equivalent paths cannot evade exclusion; registered
  parent/child destinations conflict across processes; symlinks never traversed;
  collision preflight does not publish downloaded files; changed bytes with preserved
  mtime are detected. Conditional filesystem tests must report skips explicitly.
- [x] Run `mise exec -- go test ./internal/syncengine`; observe new failures.
- [x] Implement profile `state.json` plus `profile.lock`; canonical destination
  registrations in the OS user-state directory, overridable privately for tests.
  Under a short registry lock, reject overlapping registrations and acquire a
  persistent per-destination lock; store immutable profile ownership. Registrations
  remain until an explicit future reset feature; no silent stale-registration removal.
- [x] Open destination using `os.OpenRoot` after complete remote preflight; rooted
  operations and explicit symlink rejection constrain all writes. Validate filesystem
  byte/path limits and probe case/normalization behavior with an owned temporary
  probe after remote preflight, before population. Remove only the recorded probe.
  Reject unsupported filesystems if safe validation/publication cannot be guaranteed.
- [x] Rerun subprocess tests with `-race`, required Go checks, and commit
  `feat: protect sync profiles and rooted destinations`.

## Task 8: Durable, non-replacing download execution

**Files:** create `internal/syncengine/execute.go`, publication OS files, `service.go`, tests.
**Consumes:** tasks 2, 3, 6, 7; produces `Run(context.Context, Options) (Result, error)`.

- [x] Write `TestDownloadVerified`, `TestChecksumMismatch`, `TestRemoteChangedDuringDownload`,
  `TestCancellation`, `TestNoWritesOnIncompletePreflight`, `TestNoReplaceRace`,
  `TestRecoveryBoundaries`, `TestDirectoryIntentRecovery`, and `TestSecondRunNoMedia`.
  Inject failures before/after intent, temp creation, verified hash save, publication,
  and completion save. Assert old files preserved, no unverified adoption, owned temp
  cleanup only, one final exact file, and zero second-run downloads/file writes.
  Assert disk-full/close/sync/state-write failures cannot return success.
- [x] Run `mise exec -- go test ./internal/syncengine`; observe new failures.
- [x] Implement sequential execution with phases intent → downloading → verified
  → complete. Journal the exclusive temporary path before creating it. Stream MD5
  and SHA-256, check length, refetch matching version, sync/close, save verified hash,
  publish with atomic no-replace semantics beneath the root, sync destination parent,
  then save completion. Unix hard-link publication of a closed owned temp followed
  by unlink can supply no-replace semantics; never fall back to replacing rename.
  Fail clearly if the filesystem does not support safe publication.
- [x] Recover matching final files from durable verified hashes; reject unjournaled
  files and symlinks; restart incomplete transfers from zero. Validate pending
  remote fingerprints before adoption. Complete directories through intent records.
  If the final journal save fails, return failure even if publication succeeded.
  Mark `PopulationComplete` only after every planned operation is durable; reruns
  of completed populations reject remote additions as well as changes/removals.
- [x] Implement `Run`: canonicalize, acquire profile/destination/token locks in order,
  scan completely, inspect local state, recover, plan, execute, close resources with
  checked behavioral errors. Use injectable private collaborators for fault tests.
- [x] Rerun tests with `-race`, required Go checks, and commit
  `feat: execute verified initial downloads with durable recovery`.

## Task 9: CLI services and executable workflow

**Files:** create `internal/cli/fixture.go`, `sync.go`, tests; modify root/auth tests/main.
**Consumes:** auth and fixture/syncengine services.
**Produces:** root command exposing exactly the approved spec command surface.

- [x] Write `TestFixtureFlags`, `TestSyncFlags`, `TestHelpNoCredentials`,
  `TestCommandCancellation`, and `TestCLIOutputRedacted` with injected functions.
  Assert required flags/nonempty values, no positional args, exact option forwarding,
  verify needs no token, read/write scope selection, no I/O on help, and only counts
  or private record paths in output; provider/ID/content sentinels never appear.
- [x] Run `mise exec -- go test ./internal/cli`; observe new failures.
- [x] Replace `NewRoot(AuthenticateFunc)` with `NewRoot(Services)` in root.go;
  use the exact Services and FixtureOptions contracts above. Fixture service
  closures acquire run lock before `OpenSession`; lifecycle helpers from task 5
  assume that lock is held and never reacquire it. Keep service wiring in
  `cmd/grove/services.go` with adjacent integration tests so main stays small.
  Adapt existing auth tests and main wiring. Preserve existing auth UX and cancellation.
- [x] Exercise CLI against fake endpoints in integration tests, run required Go
  checks, build, check root/subcommand help, and commit `feat: expose fixture and initial sync commands`.

## Task 10: Explicit live suite, documentation, and final verification

**Files:** create `tests/live/initial_sync_test.go`; modify mise.toml, README.md,
docs/STATUS.md, TESTBED.md, ARCHITECTURE.md, PRODUCT.md, ROADMAP.md as applicable.
**Consumes:** complete fixture/CLI/engine workflow; produces reproducible live handoff.

- [x] Write `TestLiveConfiguration` without Google calls: disabled suite skips before
  credential loading; enabled suite rejects missing/relative paths, repository paths,
  default personal token path, and token/run/profile destinations that overlap.
  Use private configuration validation so table tests do not change real credentials.
- [x] Add `TestInitialSyncLive`: create private run/profile/local directories beneath
  the configured external runs directory; load read-write session; force page size 2;
  seed → inspect → sync → independent verify → second sync. Count media requests
  in the transport and require zero on the second run. Success cleans owned objects;
  failure retains records and reports only the private record path. Optional live
  cancellation uses a separate explicitly enabled case.
- [x] Add `mise run test-live` using `go test -count=1 -v ./tests/live`; explicitly
  require `GROVE_LIVE_TEST=1` in the task. Default `go test ./...` includes the package
  but skips real API tests. Keep pinned tools and offline CI unchanged.
- [x] Document exact local auth and fixture commands, private paths, successful
  cleanup/failed-run retention, no-op criteria, supported names/types, and initial-only
  semantics. Record implemented architecture and agreed scope in the affected docs;
  add a decision record if execution changes a consequential choice rather than
  silently treating a deviation as approved.
- [x] Run `mise run fmt`, `mise run lint`, `mise run test`, `mise run test-race`,
  `mise run build`, and `mise exec -- env GOOS=linux GOARCH=amd64 go build -o
  bin/grove-linux-amd64 ./cmd/grove`; require exit 0. Run `git diff --check`.
  Cross-compilation is not evidence of Linux lock/filesystem runtime behavior;
  Linux CI must exercise those subprocess and publication tests.
- [ ] Run live tests only when explicitly enabled with the dedicated local token.
  Record actual outcomes; if authentication is unavailable, finish offline work
  and record live acceptance as pending rather than claiming full demonstration.
- [x] Commit verified code/doc changes. Review the full branch against the spec
  using the chosen execution method. Opening a PR is a separate requested action;
  if opened non-draft, wait for latest CI/CodeRabbit and assess every finding per AGENTS.md.

## Coverage and Execution Handoff

Tasks 1–3 supply safe persistence, OAuth, and Drive boundaries; tasks 4–5 deliver
the independent fixture lifecycle; tasks 6–8 deliver initial-only sync and recovery;
task 9 delivers the executable CLI; task 10 demonstrates and documents the whole
workflow. All five review-focus risks have explicit owning tests above.

Review the written plan and choose inline/native or subagent-driven execution
before implementation. Native is recommended because the tasks build sequentially
on shared contracts and keep one cohesive feature branch; perform a fresh whole-
branch review at the end. Subagent-driven execution adds independent task reviews
at the cost of fresh implementation and review contexts per task.

## Execution record

Implemented natively on `feat/initial-sync`. Runtime code uses a shared Unix
publication boundary and an injectable sync service, preserving lock-before-token
ordering. Fixture fault tests use an in-memory API, complemented by adapter HTTP
coverage and the complete CLI HTTP workflow. Destination registrations retain
ownership until a future reset operation; this limitation is documented.

Offline/race suites and formatting/lint passed; native and Linux builds passed.
Live tests remain explicitly disabled pending dedicated-account authentication.
An optional live cancellation case is deferred: cancellation/recovery has
repeatable offline coverage; the enabled live suite demonstrates the baseline.

Fresh whole-branch review reproduced three Important findings: filesystem-alias
and cross-registry exclusion, OAuth refresh redirects, and root-first fixture
creation/independent parent validation. Added RED→GREEN regressions and reran
all required offline checks. Destination parents must already exist; sibling and
inode/ancestor locks coordinate writers independently of registry configuration.
