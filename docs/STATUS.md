# Project status

Updated: 2026-10-09.

## Established

- Project name: Grove; language: Go (the user has more experience with Go).
- Repository: `git@github.com:torfstack/grove.git`; Go module:
  `github.com/torfstack/grove`.
- Linux first; Windows and macOS later.
- One-shot `grove` CLI and later `groved` daemon sharing the sync engine.
- Development tooling tracked in `mise.toml`.
- Repeatable Drive fixtures and opt-in live integration tests are agreed.
- `grove auth` is implemented with Cobra and golang.org/x/oauth2. It supports
  read-only or explicit read-write access, browser login with loopback callback,
  PKCE/state validation, cancellation, and atomic private token storage.
- Offline tests cover CLI validation, fake-provider OAuth, invalid and concurrent
  callbacks, cancellation, token persistence, and save failure preservation.
- Formatting, vet, offline tests, race tests, and builds are configured in mise.
- golangci-lint 2.14.0 is pinned in mise with an explicit five-linter rule set
  and gofmt checks. `mise run lint` checks production and test code.
- A minimal Linux GitHub Actions workflow runs lint/formatting, offline tests,
  race tests, and build on pull requests and pushes to main. GitHub CLI is pinned
  in mise for PR management.

## In progress

The user placed Desktop OAuth credentials at ~/google_client_secret.json.
No real Google token has been obtained. The first implementation is on
`feat/auth`, based on `docs/spec/000001-auth-design.md` and its approved plan.
PR preparation is in progress. An empty main baseline was created so the first
PR includes the full implementation; feat/auth now descends from that baseline.
GitHub CLI authentication works. Local lint, tests, race tests, and build passed;
the hosted Linux CI result is pending.
Requirements and design specs live in `docs/spec/`. Implementation plans live in
`docs/implementation-plans/` and reuse their corresponding spec's six-digit number.
The user approved deferring auth advisory locking. Token writes remain atomic;
locking will be revisited for sync and daemon coordination.

## Next steps

1. Complete a manual browser auth check with the dedicated test account.
2. Refine fixture tooling interfaces and plan them before implementation.
3. Seed a fresh remote run, inspect it, and
   validate initial sync against its manifest.

## Verification

Formatting, go vet, offline tests, and race tests passed. Native macOS and Linux
amd64 builds passed. Linux runtime behavior has not been tested on Linux yet.
A fresh code review found a callback response shutdown race; a regression test
reproduced EOF before the fix and passed afterward with race detection. Live
Google consent and Drive API calls remain unverified.
