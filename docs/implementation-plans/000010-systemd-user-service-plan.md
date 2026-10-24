# Systemd User-Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Preserve the user's native execution preference.

**Goal:** Ship a reusable systemd user-service template and complete installation/lifecycle instructions for the existing Linux daemon.

**Architecture:** A static instantiated unit runs groved directly with one private JSON config. Package installations use `/usr/bin/groved`; source installations override ExecStart using a user drop-in. Existing Go scheduling, locks, recovery, and status remain authoritative.

**Tech Stack:** systemd user units, Markdown, existing pinned Go tools for binary/process verification. No new dependencies.

**Spec:** [Approved spec 000010](../spec/000010-systemd-user-service-design.md).

## Global Constraints

- Ship `contrib/systemd/groved@.service`; no Go behavior changes or CLI installer.
- Map instances to `%h/.config/grove/daemons/%i.json`; use `%i`, not `%I`.
- Use `/usr/bin/groved`, `Type=exec`, `UMask=0077`, journal output, `Restart=no`, SIGTERM, `TimeoutStopSec=infinity`, and `WantedBy=default.target`.
- Do not add network-online ordering, reload, timer, D-Bus activation, or filesystem sandboxing that hides configured paths.
- Do not install/enable persistent developer-account services, enable lingering, access tokens, or perform live Drive operations during acceptance.
- Preserve unrelated changes and all existing profile/config/drop-in data. Keep credentials and synced data outside Git/logs.
- Repository is pre-release; preserve current-run safety and interruption recovery.

## Review Focus

1. Home/config paths containing spaces must remain single ExecStart arguments: Task 2 checks manager-parsed execution.
2. Escaped instance names must not become path traversal: Task 2 checks `%i` versus literal escaped names.
3. Existing local config/drop-ins must survive repeated setup: Task 1 documents explicit refusal/review before replacement; Task 2 exercises repeat setup in a temporary tree.
4. Fatal startup failures must stay failed without automatic restart: Task 2 uses actual groved with an invalid disposable config.
5. Service stop must permit cleanup rather than force a timeout kill: Task 2 checks an intentionally delayed SIGTERM handler; existing daemon process tests cover actual engine cancellation.

## Files and responsibilities

- Create `contrib/systemd/groved@.service`: package-ready user-service template.
- Create `contrib/systemd/README.md`: installation, source override, lifecycle, upgrades/removal, custom paths, and linger guidance.
- Modify root `README.md`: fix stale daemon overview, link the detailed guide, remove systemd from future-work list once shipped.
- Modify `docs/ARCHITECTURE.md`, `docs/PRODUCT.md`, `docs/ROADMAP.md`, `docs/TESTBED.md`, and `docs/STATUS.md`: implemented integration, validation evidence, limitations.
- Create `docs/decisions/0010-systemd-user-service.md`: record the approved packaging/lifecycle choices and their tradeoffs.
- Update paired spec and this plan with actual completion state.
- Runtime acceptance artifacts belong in private temporary directories; no permanent test harness unless exercising the lifecycle exposes a recurring need.

### Task 1: Ship the template and operator guide

**Interfaces:** Consumes `groved --config ABSOLUTE_PATH` and version-1 daemon JSON from spec 000009. Produces `groved@NAME.service` and the instance-specific `groved@NAME.service.d/override.conf` source-build contract; creates no application service API.

- [x] Create the template with a fixed description including `%i`, quoted executable/config arguments, the exact settings in Global Constraints, explicit `KillSignal=SIGTERM`, and normal control-group termination. No shell, PID file, custom working directory, credentials in environment, or ExecStartPre helper.
- [x] Write prerequisites and a `personal` instance walkthrough in `contrib/systemd/README.md`. Install a source-built binary to `~/.local/bin/groved`; provide a per-instance override with an empty `ExecStart=` followed by the quoted replacement command. Explain package installs omit that override.
- [x] Provide commands to create private directories/config and copy the template without silently overwriting existing files. Use quoted shell paths, explicit existence checks/refusal, and editor steps for user-specific values. Re-running setup must direct the user to review existing files. Do not change an existing binary while its service is running; document stop/update/restart.
- [x] Document fixed `%h/.config` lookup regardless of XDG_CONFIG_HOME, simple instance labels, literal `%i` handling, and an absolute-config-path override. Explain systemd argument quoting and literal `%%`/`$$` escaping independently from shell quoting; link official unit/exec documentation. Do not suggest shell expansion in ExecStart.
- [x] Document `systemctl --user daemon-reload`, start, enable, status, stop, restart, disable, and `journalctl --user -u groved@personal.service`. Explain Type=exec is not sync readiness; pair service status with offline `grove status`. Ordinary attempt failures remain in the daemon loop; fatal failures and unexpected death need explicit operator restart.
- [x] Document SIGTERM cleanup, potentially indefinite stop/restart, stale history/recovery after forced death, manual repair workflow, lack of reload, copied-unit upgrades, and safe removal retaining profiles/tokens/JSON/data. Avoid implying systemd status contains per-attempt sync logs.
- [x] Document optional linger and its inverse, permission requirements, effects on other user services, encrypted-home/mount limits, and that enabling a user unit does not itself enable lingering.
- [x] Fix the root README overview and link the guide. Check relative links and review every command against the actual binary/config contracts. Run `git diff --check`; expect exit 0.
- [x] Update STATUS with shipped assets and verification still pending. Commit the bounded asset/docs change if working in the implementation branch.

### Task 2: Verify manager behavior and actual daemon failure visibility

**Interfaces:** Consumes Task 1's exact template, override, and setup steps. Produces recorded evidence; does not alter daemon behavior. Use unique temporary unit names and runtime-only files so normal user units are untouched.

- [x] Inspect `systemctl --user` connectivity and `systemd-analyze --version`. If the manager is unavailable, retain static verification and existing process-test coverage and record the runtime limitation; do not start a new persistent manager or request elevated service access merely to hide that gap.
- [x] Build with `mise run build`. Run `systemd-analyze --user verify` on the template and a disposable source-build layout. Account explicitly for absent `/usr/bin/groved` by validating a temporary package-layout fixture or executable override; do not modify `/usr/bin`. Capture actual warnings and require no unexplained diagnostics.
- [x] Where a manager is available, load uniquely named temporary runtime test units based on the shipped template using an isolated test config/home location and an absolute executable override. Keep a list of every created file/unit; install no default.target wants links. Always stop/unload units and remove only test-owned files, including on failure. Bound observation waits; do not leave an infinity-stop test running if its handler fails.
- [x] Use a harmless temporary helper that reports argv to a private file, emits a fixed journal marker, and delays exit briefly after SIGTERM. Verify config is one argument when the synthetic home has spaces; test an escaped instance label and assert no unescaping into a path separator. Inspect effective Type, Restart, KillSignal, TimeoutStopUSec, UMask, and output settings. This proves manager mechanics, not real sync.
- [x] Stop the helper unit, verify its cleanup marker appears before inactivity and no signal-kill result occurs. Separately let it fail and verify it remains failed with zero automatic restarts. Run the documented setup twice against a temporary tree; existing config and override contents must remain byte-identical on the refused second attempt.
- [x] Replace the helper override with the built actual groved and a disposable invalid config outside Git. Assert nonzero failed service status, fixed sanitized journal error, no automatic restart, and no token/profile/network use. Service activation alone must not be reported as successful daemon validation.
- [x] List existing daemon process tests with `mise exec -- go test ./cmd/groved -list Test`; run the relevant ownership/signal/cancellation tests using their actual names and `-count=1`. Expect pass. If Go changes unexpectedly become necessary, stop and reassess scope; any such changes require fmt/lint/full tests and race checks per AGENTS.md.
- [x] Record static, helper-manager, actual-groved startup, and existing real-daemon test results separately in TESTBED/STATUS, including any unavailable checks. No authenticated systemd sync acceptance is claimed.

### Task 3: Reconcile project records and review the complete slice

**Interfaces:** Consumes Task 2 evidence. Produces an accurate implemented/verified boundary across project docs.

- [ ] Add accepted decision 0010 covering package/template distribution, fixed config convention, source override, explicit restart, and unlimited graceful stop; identify these as the user's approved spec choices.
- [ ] Update architecture/product/roadmap to describe the shipped template while retaining watching and other deferred features. Update spec and plan completion only to the extent verified; preserve historical decision 0008's original scope.
- [ ] Run `git diff --check`, check all touched relative documentation links, and inspect the complete diff against spec 000010. Confirm no accidental installed service, enabled linger, credential access, generated IDs, synced data, or temporary acceptance artifacts remain in the worktree.
- [ ] Request one fresh read-only whole-change review under the native execution workflow. Assess findings against the approved scope, fix valid issues, rerun affected verification, and record outcomes/limits in STATUS. No PR, external comment, push, or merge is implied by this plan.
- [ ] Commit the completed verification/docs changes in the implementation branch and report the shipped behavior, actual results, and runtime limits.

## Self-review and handoff

All approved spec sections map to Tasks 1–3; the five Review Focus conditions
have explicit acceptance steps. The plan introduces no Go interfaces or new
dependencies. Runtime-manager tests remain conditional on availability and do
not substitute helper behavior for actual sync acceptance.

Native execution is retained from the existing session preference recorded in
STATUS. The user approved this plan for execution. Tasks 1–2 are verified; Task 3
project records are updated and the independent whole-change review is next.
