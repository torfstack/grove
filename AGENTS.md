# Working on Grove

## Session workflow

- Read `docs/STATUS.md`, `docs/ROADMAP.md`, and relevant design and decision records.
- Inspect the working tree before editing; preserve unrelated user changes.
- Work on a bounded task with clear completion criteria.
- Run checks appropriate to the change and report actual results and limitations.
- Update `docs/STATUS.md` before ending substantive work. Update affected product,
  architecture, roadmap, and decision documents when their contents change.
- Record consequential choices in `docs/decisions/`; distinguish agreed decisions
  from proposals and open questions.
- Store requirements and design specs in `docs/spec/`. Assign each new spec the
  next unused six-digit counter, starting at `000001`, followed by a descriptive
  name. Keep existing numbers stable.
- Store implementation plans in `docs/implementation-plans/` and reuse the
  corresponding spec's number: `docs/spec/000001-auth-design.md` pairs with
  `docs/implementation-plans/000001-auth-plan.md`. Link each plan to its spec.
  Creating a plan does not advance the spec counter. These conventions override
  skill defaults for document paths and date-based filenames.

## Development

- Use Go. Keep development tools pinned in `mise.toml`; use the same versions in CI.
- Keep code comments sparse. Prefer readable names and small, straightforward
  functions. Comment only when explaining a non-obvious reason or constraint
  materially helps the reader; do not narrate what the code already expresses.
- Deliver Linux first while keeping platform-specific behavior behind boundaries.
- Share the sync engine between `grove` and `groved`.
- Prefer deterministic tests for sync planning and failure recovery.
- For Go changes, run `mise run fmt`, `mise run lint`, and `mise run test`.
  Use `mise run test-race` where concurrency is involved and supported.
  Keep `.golangci.yml`'s linter list explicit; preserve gofmt as the formatter.
  Check errors that affect behavior; explicitly discard only best-effort errors.
- Live Drive tests must be explicitly enabled and use the dedicated test profile,
  disposable local directories, and fixture-owned remote data.
- Keep credentials, tokens, generated Drive IDs, and synced data out of Git and
  logs. Do not print token contents during inspection.
- Never infer that a missing or incomplete remote listing authorizes deletion.
- Do not allow concurrent writers for the same sync profile.

## Pull request feedback

- After opening or updating a non-draft PR targeting `main`, wait for CI and
  CodeRabbit to finish reviewing the latest commit. Keep the user informed while
  waiting; do not report the PR as ready while its review is pending.
- Retrieve CodeRabbit's review summary, inline comments, and review threads
  through GitHub. A successful review check alone does not mean there are no
  findings. Check that the feedback applies to the latest commit.
- Assess each finding against the code, requirements, and agreed scope. Fix
  valid findings with appropriate verification; explain why any suggestion is
  declined or deferred. Do not apply suggestions blindly.
- Push fixes within the authorized PR work, then wait for fresh CI and review
  feedback. Repeat until checks pass and all actionable findings are addressed.
- If review is skipped, unavailable, or stalled, investigate and report the
  remaining limitation explicitly. Record unresolved findings and the next step
  in `docs/STATUS.md` so another session can continue.
- Summarize review outcomes to the user. Posting replies or resolving GitHub
  threads requires authorization to communicate there; merging requires explicit
  user authorization.
