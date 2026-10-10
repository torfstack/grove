# Drive testbed

The agreed approach is a dedicated disposable Google account plus a versioned
fixture manifest. The baseline workflow is implemented on `feat/initial-sync`; real Drive execution
remains pending local test-account authentication.

## Google setup (user)

1. Create a Google account holding only disposable test data.
2. Create a Cloud project and enable the Google Drive API.
3. Configure OAuth as External / Testing and add that account as a test user.
4. Create a Desktop app OAuth client for the initial Linux target and download
   its JSON credentials. Store them outside Git.

Use browser-based user OAuth, with a locally stored refresh token. The account
password is never required by Grove or the assistant. Implement a desktop
loopback authorization flow; the quickstart's sample authentication code is not
the specification for our production flow.

Read-only initial download tests can use `drive.readonly`; two-way operations
need `drive`. `drive.file` does not grant access to arbitrary existing content.
OAuth scopes cannot restrict the full Drive scope to our fixture folder; that
boundary must be enforced by the test harness. External / Testing refresh tokens
using Drive scopes expire after seven days, so reauthentication is expected.

Sources: [Go quickstart](https://developers.google.com/workspace/drive/api/quickstart/go),
[Drive scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth),
[token expiration](https://developers.google.com/identity/protocols/oauth2#expiration).

## Repeatable fixtures

Commit a manifest with logical entry IDs, parent relationships, Drive names,
entry types, deterministic content, and content hashes. Use relationships rather
than paths as identity because Drive permits duplicate sibling names.

Each seed run creates a fresh remote root and a local run record mapping logical
IDs to actual Drive IDs. Generated IDs, tokens, and run state stay outside Git.
Local downloads use a fresh temporary directory and a dedicated Grove profile.

Start with a portable baseline: nested and empty folders, zero-byte files, text,
binary content, and Unicode names. Verify membership, sizes, and content hashes;
do not require identical timestamps. Run sync twice and verify the second pass
does no unnecessary work.

Keep duplicate sibling names in a separate edge-case fixture until name mapping
is specified. Later add incompatible names, shortcuts, Google-native documents,
sharing, remote changes, and concurrent edits with explicit expected outcomes.

Seeding, inspection, verification, and cleanup are separate operations. Cleanup
targets only objects recorded as belonging to that run; it must never search by
folder name alone or clear the account wholesale. Failed runs retain diagnostic
records without credentials for investigation.

## Test layers

- Fast offline tests cover manifest validation, planner decisions, simulated
  network failures, and recovery behavior.
- Live tests are explicitly enabled, exercise the real Drive API, and verify
  fixtures using expected content rather than the sync engine's own conclusions.
- Live tests are not part of the default offline test suite. Public CI receives
  no test account credentials by default.

## Completion criteria for the first testbed implementation

Authenticate as the test account, seed the baseline fixture, download it to an
empty local directory, verify expected content, and repeat the sync without
unnecessary transfers. Record failures and preserve enough run state to inspect
them. Command syntax and private records are defined in
[spec 000002](spec/000002-initial-sync-design.md) and implemented by the paired plan.

Run `mise run test-live` with `GROVE_LIVE_TEST=1`, an absolute dedicated
`GROVE_TEST_TOKEN_FILE`, and an absolute `GROVE_TEST_RUNS_DIR` outside Git. The
suite uses page size 2, seeds a new root, independently verifies content, checks
zero second-run media requests and unchanged local mtimes, then cleans up on
success. Failed runs retain their private records and remote data. Offline tests
cover interruption/recovery and use a local HTTP Drive server for the full CLI
workflow. Public CI does not receive credentials. See README for executable setup.
