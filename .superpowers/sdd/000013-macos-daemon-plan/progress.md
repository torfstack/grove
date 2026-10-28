# SDD ledger — plan: docs/implementation-plans/000013-macos-daemon-plan.md

Plan baseline: `0f4b3ae4c2f40010c83f946fc1e44c43cd64e10e` (approved design and plan). Implementation branch was created from current `origin/main` at `357f2398e55d89deda6d4779ddb03f58ff7d3786`.

Pre-flight shared interfaces:
- Task 1 produces Darwin daemon runtime/config/watch support consumed by Task 7 process acceptance; existing `Run` already shares the daemon and sync engine, so no interface conflict.
- Task 2 produces generic tray model/options consumed by Tasks 3 and 5; the current Linux coordinator and command both carry `SystemdInstance`, so both consumers must move in sync to `ServiceInstance`.
- Task 3 produces the generic coordinator and `OpenBus`/`OpenService` dependency boundaries consumed by Task 5. Current default `OpenService` creates D-Bus inside `Run`; move that setup into Linux adapter as planned.
- Task 4 produces the Darwin `Controller` and daemon LaunchAgent template consumed by Tasks 5, 6, and 7. The spec requires Stop only for a job verified running with active profile ownership.
- Task 5 produces the menu-bar command/adapter consumed by Task 6 bundle packaging and Task 7 lifecycle acceptance.
- Task 6 produces `Grove.app`, plist templates, and build task consumed by Task 7 CI/build coverage.

Ruling: Stop requires parsed launchd state `running` as well as active profile ownership — the spec explicitly requires both, while Task 4 Step 3's abbreviated wording only says “verified loaded”; cost if wrong: an inconsistent loaded-but-stopped job could be booted out or status could offer an unsafe control.

Environment: implementation workspace is Linux; Darwin tests can be cross-compiled here, but native AppKit/launchd acceptance requires a macOS runner. No Drive live tests are authorized or planned.
Task 1: complete (commits 0f4b3ae..e010167, tests: env TMPDIR=/run/user/1000 go test ./internal/daemon ./internal/privatefs ./cmd/groved → ok  	github.com/torfstack/grove/cmd/groved	(cached))
Task 1 native macOS tests are not executable in this Linux workspace; Darwin arm64 test binaries for daemon, groved, and privatefs cross-compiled successfully and the macOS CI added in Task 7 will provide native execution.
Task 2: complete (commits e010167..7fec336, tests: env TMPDIR=/run/user/1000 go test ./internal/tray → ok  	github.com/torfstack/grove/internal/tray	(cached))
Ruling: move the Linux CLI flag rename (`--systemd-instance` → `--service-instance`) into Task 2 — Task 2's accepted docs change and platform-neutral `Options` field otherwise leave a broken intermediate CLI; Task 5 will reuse the already-generic option. Cost if wrong: the flag change lands one task earlier than the plan's original sequencing.

Baseline environment: plain `go test ./...` failed because `/tmp/.git` is present and Grove rejects all test temporary paths beneath `/tmp`; the same run also showed the existing Linux tray process tests need a live SNI host. Setting `TMPDIR=/run/user/1000` fixed private-path failures. Task 1 targeted suite and Task 2 tray/CLI suites passed; no app behavior finding from those baseline environmental failures.
Task 3: complete (commits 7fec336..cf6abfa, tests: env TMPDIR=/run/user/1000 go test ./internal/tray → ok  	github.com/torfstack/grove/internal/tray	(cached))
Task 4: implementation complete; controller/parser/template checks pass (`TMPDIR=/tmp/grove-task-tmp go test ./internal/tray -run 'TestLaunchd' -count=1`, `go test -race ./internal/tray`). Native disposable-agent acceptance deferred to Task 7 because this workspace is Linux-only. The plist template test was added first and observed failing because the file did not exist, then passed after template creation.
Ruling: revalidate the current private plist on loaded jobs and require its arguments to equal launchctl's effective argument vector before exposing controls — `bootout` uses the plist path, so changed plist content must fail closed; cost if wrong: a legitimate externally edited plist requires reloading before tray controls return.
Task 5: implemented (`514c056`); Linux tray/CLI tests and race checks pass. Darwin menu mapping tests are present but cannot run or compile on this Linux host because systray requires Darwin cgo/AppKit. The package's attempted Darwin CGO-disabled cross-compile failed on missing systray native symbols; macOS CI must provide actual compilation/runtime validation.
Task 6: implementation complete; Linux build, static tests, XML parsing, and diff checks pass. Native `plutil` unavailable on this Linux host; macOS CI will run native plist validation.
Task 7: Linux fmt, lint (0 issues), complete offline test suite, race suite, build, XML parse, and diff checks pass. CI now runs native macOS checks and owned LaunchAgent acceptance tests. Darwin tests cannot run from this Linux workspace; hosted results are pending. No live Drive credentials were used and no real user launchd jobs were changed locally.
Ruling: defer interactive menu-bar appearance/login acceptance to a macOS desktop user check — the implementation host is Linux and hosted CI has no interactive user desktop; cost if wrong: visual or login-session behavior may need follow-up before release.
CodeRabbit initial review completed on commit `32e5f48`: five actionable findings, all valid and fixed locally (real AppKit event loop; platform-bound watcher helpers; removed menu-item listener cleanup; canonical temp fixture path; deferred context cancel). The macOS lint failure independently reported the first two code issues and the context leak. No follow-up CodeRabbit review requested per project instruction.
