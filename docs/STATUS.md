# Project status

Updated: 2026-10-10.

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

Fixture tooling and initial download sync are implemented together on
`feat/initial-sync`, following approved [spec 000002](spec/000002-initial-sync-design.md)
and its [plan](implementation-plans/000002-initial-sync-plan.md). Native execution
completed private records/locks, persistent OAuth refresh, Drive HTTP access,
fixture lifecycle, deterministic planning, rooted downloads and recovery, CLI
wiring, and the opt-in live suite. Offline and race tests, lint, formatting, native
build, and Linux cross-build passed. Real Drive access remains unverified; the live
suite was not enabled and no user token was inspected. Whole-branch independent
review and the requested PR's CI/CodeRabbit review are next.

The user previously placed Desktop OAuth credentials at ~/google_client_secret.json.
Live authentication remains unverified in this session. The initial auth
implementation and Linux CI from
[PR #1](https://github.com/torfstack/grove/pull/1) are merged into main.
Requirements and design specs live in `docs/spec/`. Implementation plans live in
`docs/implementation-plans/` and reuse their corresponding spec's six-digit number.
The user approved deferring auth advisory locking. Token writes remain atomic;
locking will be revisited for sync and daemon coordination.

## Next steps

1. Complete a manual browser auth check with the dedicated test account.
2. Complete whole-branch review, open the requested PR, and assess CI/CodeRabbit.
3. Seed a fresh remote run, inspect it, and
   validate initial sync against its manifest.

## Verification

Agent PR workflow now requires waiting for CI and CodeRabbit, retrieving feedback,
and addressing valid findings before reporting readiness. CodeRabbit's docstring
coverage quota and generation suggestion are disabled to match our sparse-comment
policy; useful documentation feedback remains welcome.

Formatting, go vet, offline tests, and race tests passed. Native macOS and Linux
amd64 builds passed. The hosted CI workflow runs the offline suite on Linux;
see PR #1 for its current result. Real Linux browser integration is still untested.
A fresh code review found a callback response shutdown race; a regression test
reproduced EOF before the fix and passed afterward with race detection. Live
Google consent and Drive API calls remain unverified.

## Repository housekeeping

Expanded `.gitignore` to cover Go build and test output, editor and local agent
state, OS metadata, and environment files while preserving existing project
credential and binary rules. Go source, module files, mise configuration, and
environment examples remain trackable. Verified with `git check-ignore` and
`git diff --check`; no Go code changed.

## Initial sync verification

Offline CLI HTTP integration demonstrates seed → inspect → sync → independent
verify → no-op repeat → cleanup with page size 2. Recovery tests inject journal
write failures at intent, transfer, verified-hash, and completion boundaries;
checksum/version changes, cancellation, and racing destinations fail safely.
Process tests exercise token/profile/destination exclusion on macOS; Linux runtime
coverage will run through CI. The Linux cross-build is compilation evidence only.
The live acceptance command and dedicated-token setup are documented in README.
