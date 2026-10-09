# Grove

A Google Drive sync client written in Go. Linux is the first supported
platform; Windows and macOS are planned.

The planned `grove` CLI performs individual operations such as authentication,
sync, and status. A later `groved` daemon schedules the same shared sync engine.
`grove auth` is implemented. Sync, status, and the daemon are planned.

Development tools are pinned in [mise.toml](mise.toml). Install them with
`mise install`.

Build and test:

```sh
mise run build
mise run test
mise run test-race
mise run vet
mise run lint
```

Formatting uses `mise run fmt` (gofmt). The pinned golangci-lint checks formatting,
unchecked errors, suspicious code, unused code, and ineffective assignments.
Its explicit rule set is in `.golangci.yml` and includes test files.

GitHub Actions runs lint/formatting checks, offline tests, race tests, and a build
on Linux for pull requests and pushes to `main`. It uses the versions in
`mise.toml`; live Drive tests are separate.

CodeQL analyzes Go and Actions workflows on PRs, main pushes, and a weekly schedule.
Dependency review rejects newly introduced known high or critical vulnerabilities.
Dependabot checks Go modules and Actions weekly, grouping minor/patch updates and
limiting routine PRs to three per ecosystem. These Dependabot schedules become
active when the configuration reaches `main`.

GitHub secret scanning and push protection are enabled. CodeRabbit's GitHub app
is installed and configured for advisory reviews of PRs targeting `main`.

Authenticate with a Google Desktop app client JSON:

```sh
./bin/grove auth --client-secret ~/google_client_secret.json
```

This opens the system browser and requests read-only Drive access. To allow
fixture creation and future two-way operations, explicitly request write access:

```sh
./bin/grove auth --client-secret ~/google_client_secret.json --access read-write
```

The command also prints a URL you can open manually on the same machine if browser
launch fails. It waits up to five minutes; Ctrl-C cancels. Use the dedicated test
account for development.

Tokens are plaintext files with owner-only permissions. The default path is
`$XDG_CONFIG_HOME/grove/token.json` (or `~/.config/grove/token.json`) on Linux and
`~/Library/Application Support/grove/token.json` on macOS. Use `--token-file PATH`
to isolate a test run. Credentials remain at their original location; moving
them will require updating configuration or authenticating again. Every auth
invocation requests consent again and replaces the token only after success.
With simultaneous auth invocations, the last successful atomic write wins.

Google External apps in Testing mode issue Drive refresh tokens that expire after
seven days; rerun auth when needed. Windows auth is not implemented yet.

Start with [project status](docs/STATUS.md), [product scope](docs/PRODUCT.md),
[architecture](docs/ARCHITECTURE.md), and [roadmap](docs/ROADMAP.md).
The [Drive testbed](docs/TESTBED.md) describes live testing.
