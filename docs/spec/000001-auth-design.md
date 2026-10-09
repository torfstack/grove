# Grove authentication design

Date: 2026-10-09. Status: approved for implementation planning.

## Intent and scope

Provide the first `grove auth` command so the user can authorize the dedicated
Google test account through their system browser. Persist credentials for later
Drive commands without exposing tokens in console output or Git. Linux is the
first supported target; this session's development host is macOS.

This delivers OAuth authorization for Drive, not a separate OpenID login feature.
Account identity display and multiple profiles are deferred.

## Command

`grove auth --client-secret ~/google_client_secret.json`

The client-secret flag is required, accepting Google's Desktop client JSON.
Default access is `drive.readonly`; `--access read-write` requests `drive` for
fixture creation and future two-way sync. Reject other access values.
No implicit scope upgrades occur in later commands.

Default token storage is `grove/token.json` under the OS user configuration
directory (on Linux, honor XDG_CONFIG_HOME). `--token-file` overrides this for an
isolated test profile. Do not copy the original client-secret file. Store its
absolute path with the token so later commands can load the client configuration;
if moved or missing, report an actionable error.

An explicit auth invocation performs authorization again. A successful result
replaces the previous token; cancellation or failure preserves it.

## Browser authorization flow

1. Validate client JSON as a Desktop client and use Google's documented OAuth
   endpoints; do not accept arbitrary endpoints from the JSON.
2. Listen on 127.0.0.1 at a random available port before opening the browser.
3. Generate cryptographically random state and an S256 PKCE verifier/challenge.
4. Request the chosen Drive scope, offline access, account selection, and consent
   so the resulting authorization supplies a refresh token.
5. Open the system browser through a platform adapter. Print the authorization
   URL for manual opening if necessary. Do not launch a shell with URL interpolation.
6. Accept only the expected GET callback with valid state and a single code or
   OAuth error. Reject malformed or unrelated callbacks without exchanging codes.
7. Exchange the code with the same redirect URI and PKCE verifier. Require an
   access token and refresh token before storing the result.
8. Show a plain browser completion message and report the saved token path in
   the CLI. Never echo codes, tokens, or raw token endpoint response bodies.

Use a five-minute total deadline and support Ctrl-C cancellation. Shut down the
listener and browser-launch helper resources on all exit paths. An invalid-state
request must not terminate a legitimate pending login. Consume a valid callback
only once, including when callbacks arrive concurrently.

## Persistence and boundaries

Use golang.org/x/oauth2 for OAuth requests and refresh behavior. Keep CLI parsing,
browser launch, OAuth flow, and token storage separate so tests can substitute
each external boundary. Choose the module path in the implementation plan;
no repository hosting URL has been supplied yet.

Store a versioned record containing the OAuth token, requested scopes, client ID,
and client configuration path. Use owner-only permissions (0700 for newly created
Grove directories and 0600 for token files on Unix). Write through a temporary
file in the destination directory, then replace atomically; reject a symlink
destination. Windows credential storage and replacement semantics require their
own implementation before claiming Windows support.

Advisory locking is deferred until sync and daemon coordination need it. Atomic
replacement protects against partial token files; if two auth commands succeed
for the same destination, the last successful write wins.
Keychain integration is deferred; tokens are plaintext owner-only files initially.

Future Drive clients use a persistent token source: successful refreshes update
the stored token atomically and retain the existing refresh token if the response
omits it. This is a later integration requirement, not a Drive command in this slice.

## Verification

Offline tests use a fake OAuth provider and injected browser opener, with no
Google credentials. Cover the URL's scopes, offline request, state and PKCE;
successful callbacks and code exchange; wrong state, duplicate callback fields,
denied consent, timeout and cancellation; missing refresh token; storage errors,
permissions, atomic replacement, and preservation of an existing token on
failure. Verify PKCE at the fake token endpoint rather than just inspecting code.

Run formatting, go vet, unit tests, and race detection for the callback flow.
A manual live check completes browser consent with the dedicated test account
and confirms persistence without printing tokens. It is opt-in, not a default test.

## Alternatives considered

- Browser loopback OAuth: selected; follows desktop guidance and exercises the
  eventual CLI experience.
- Pasting authorization codes: not selected; avoids relying on the discontinued
  out-of-band redirect flow.
- Device authorization: not selected for this desktop Drive client; do not assume
  Google's limited-input device flow supports the required scopes.

Reference: [Google desktop OAuth guidance](https://developers.google.com/identity/protocols/oauth2/native-app).
