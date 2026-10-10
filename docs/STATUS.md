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
build, and Linux cross-build passed. Dedicated-account live acceptance passed on
macOS on 2026-10-10: seed, inspect, sync, independent verification, no-op repeat,
and fixture-owned remote cleanup. Token contents were not printed. Whole-branch independent
review found three valid issues (filesystem-alias exclusion, refresh redirects,
and fixture root ordering); regression tests reproduced them and all are fixed.
[PR #5](https://github.com/torfstack/grove/pull/5) is open. Linux CI and security
checks passed on the original implementation. CodeRabbit's three findings are
fixed: fixture cleanup now processes the root last, destination preflight checks
hardlink publication support, and the auth exclusion test reaches the token lock.
Formatting, lint, offline tests, and race tests passed after these fixes. Latest
main (Dependabot PRs #2–#4) is merged into the branch. Linux CI, dependency review,
CodeQL, and CodeRabbit passed on `29e775e`. CodeRabbit's latest review generated no
actionable comments and marked the three original threads resolved. Live
acceptance passed with the user; PR #5 remains open and unmerged.

The user previously placed Desktop OAuth credentials at ~/google_client_secret.json.
Dedicated test-account browser authentication succeeded. The initial auth
implementation and Linux CI from
[PR #1](https://github.com/torfstack/grove/pull/1) are merged into main.
Requirements and design specs live in `docs/spec/`. Implementation plans live in
`docs/implementation-plans/` and reuse their corresponding spec's six-digit number.
Authentication and sync token sessions now share advisory locking. Token writes
remain atomic, and concurrent writers for a token or sync profile are refused.

## Next steps

1. Review and merge PR #5 when explicitly authorized.
2. Design incremental sync before expanding the initial-only behavior.

## Verification

Dependabot PR #2 now pins CodeQL `init` and `analyze` to the same revision,
`24c54180a607b1449ed407dd24f251e4e9147c8d`, addressing CodeRabbit's finding
about unsupported mixed versions. The workflow-only fix preserves existing
inputs, permissions, and Go tooling. Fresh hosted CI and CodeRabbit review are
pending after pushing the fix; inspect both before reporting readiness.

Agent PR workflow now requires waiting for CI and CodeRabbit, retrieving feedback,
and addressing valid findings before reporting readiness. CodeRabbit's docstring
coverage quota and generation suggestion are disabled to match our sparse-comment
policy; useful documentation feedback remains welcome.

Formatting, go vet, offline tests, and race tests passed. Native macOS and Linux
amd64 builds passed. The hosted CI workflow runs the offline suite on Linux;
see PR #1 for its current result. Real Linux browser integration is still untested.
A fresh code review found a callback response shutdown race; a regression test
reproduced EOF before the fix and passed afterward with race detection. Live
Google consent and the baseline Drive workflow are now verified on macOS;
Linux browser and live Drive integration remain untested.

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

The enabled live suite passed in 40.70 seconds using a dedicated local token.
Home contains a `.git/hooks` directory used by global `core.hooksPath`, so the
conservative outside-Git guard rejected the documented home paths. The successful
run used private temporary paths outside that marker. Its remote fixture objects
were trashed; local records remain private. An earlier sandboxed attempt failed
at the first create. Authenticated inspection found no matching remote object;
its pending record is retained because an absent listing does not resolve an
uncertain create. Token contents and generated remote IDs are not recorded here.
