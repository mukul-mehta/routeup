# AGENTS.md

This file tells AI agents how to work in the `routeup` repository.

## Project Context

`routeup` is a Go CLI and networking tool that gives local services stable HTTPS route names and can expose those routes publicly through a self-hostable server.

Core docs:

- `PLAN.md` records product and architecture decisions.
- `docs/ARCHITECTURE.md` explains the system design.
- `docs/ENGINEERING-STANDARDS.md` defines code quality rules.
- `docs/MILESTONES.md` defines implementation phases.

Read those docs before changing implementation plans or writing code.

## Collaboration Rule

The owner wants to write the implementation code personally.

Default agent behavior:

- Do not generate large implementation slices unless explicitly asked.
- Prefer plans, interfaces, test outlines, review comments, and debugging help.
- When asked to implement, make the smallest correct change for the current milestone only.
- Do not pull future milestone work into the current slice.
- Keep docs and code behavior aligned.

Good assistance examples:

```txt
Review my route parser.
Suggest tests for this registry behavior.
Help me debug this tunnel cancellation issue.
Design the local agent API shape.
Update docs for this command decision.
```

Bad assistance examples:

```txt
Implement the whole tunnel stack without being asked.
Add a plugin system because it might be useful later.
Introduce a database before persistence needs are proven.
```

## Recommended Go Skills (Optional)

The following Go skills come from `samber/cc-skills-golang`. They sharpen agent guidance on Go-specific topics. They are optional — if your agent environment does not have them installed, skip them and rely on standard Go practices and the engineering rules in `docs/ENGINEERING-STANDARDS.md`.

When working on Go code, load any installed skills below before giving implementation advice.

Always relevant when available:

- `golang-how-to`
- `golang-code-style`
- `golang-error-handling`
- `golang-safety`
- `golang-documentation`

Load additionally when relevant:

- CLI command tree: `golang-cli`, `golang-spf13-cobra`
- Config layering: `golang-spf13-viper` only for comparison; prefer a small custom loader initially
- Goroutines, tunnels, shutdown: `golang-concurrency`, `golang-context`
- HTTP, TLS, tokens, route claims: `golang-security`
- Tests and examples: `golang-testing`
- CI setup: `golang-continuous-integration`

## Engineering Rules

Follow `docs/ENGINEERING-STANDARDS.md`.

Key reminders:

- Prefer the Go standard library unless a dependency clearly earns its weight.
- Use `context.Context` first for I/O, network, tunnel, process, and long-running functions.
- Wrap errors with `%w` and useful operation context.
- Use `log/slog` outside CLI output paths.
- Every goroutine must have a shutdown path.
- Do not log request bodies, cookies, authorization headers, or webhook signatures by default.
- Avoid package names like `utils`, `common`, or `helpers`.
- Keep exported surface minimal.

## Verification

Once Go code exists, baseline verification is:

```bash
go test ./...
```

Implementation responses should report:

- Tests run and results.
- Manual verification, if behavior is user-visible.
- Skipped tests and why.
- Known limitations left for later milestones.

## Review Style

When asked for review, findings come first and must be ordered by severity. Include file and line references when possible.

Focus on:

- Bugs and behavioral regressions.
- Security issues in tokens, route claims, TLS, logs, and Host routing.
- Goroutine leaks and missing cancellation.
- Missing tests.
- Scope creep beyond the active milestone.
