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
- Dependabot alerts and security updates are enabled. Secret scanning and push
  protection were already enabled and were verified through the GitHub API.
- Dependabot weekly version updates for Go and Actions, CodeQL for Go and Actions,
  and high/critical dependency review are prepared in PR #1. Version-update
  schedules become active after merging the configuration to main.
- CodeRabbit advisory review configuration is prepared. The user has no paid
  Copilot plan; use CodeRabbit's free reviews for this public repository instead.
  Its GitHub app is installed; an initial skipped-review notice showed the
  repository configuration was loaded. Main is explicitly enabled for reviews.

## In progress

The user placed Desktop OAuth credentials at ~/google_client_secret.json.
No real Google token has been obtained. The first implementation is on
`feat/auth`, based on `docs/spec/000001-auth-design.md` and its approved plan.
[PR #1](https://github.com/torfstack/grove/pull/1) contains the initial auth
implementation and Linux CI. An empty main baseline lets the PR include the full
implementation; feat/auth descends from that baseline. Both branches are pushed.
GitHub CLI authentication works. The PR checks show the current hosted CI result.
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
amd64 builds passed. The hosted CI workflow runs the offline suite on Linux;
see PR #1 for its current result. Real Linux browser integration is still untested.
A fresh code review found a callback response shutdown race; a regression test
reproduced EOF before the fix and passed afterward with race detection. Live
Google consent and Drive API calls remain unverified.
