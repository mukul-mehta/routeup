# Engineering Standards

This document defines implementation standards for `routeup`. All implementation work must follow this file in addition to `AGENTS.md`, `PLAN.md`, `docs/ARCHITECTURE.md`, and `docs/MILESTONES.md`.

## Must-Haves

- Use the smallest correct implementation that satisfies the current milestone.
- Keep the route model simple: dotted route names such as `charpai` and `api.charpai`.
- Prefer explicit, boring code over clever abstractions.
- Do not add compatibility layers, plugin systems, or extension points before there is a concrete need.
- Write tests for implementation work unless the change is docs-only, generated-only, or explicitly test-impractical.
- Keep tests next to the code unless external package testing is intentional.
- Use table tests where they improve clarity.
- Keep docs updated when command behavior, route semantics, or architecture decisions change.
- The owner writes implementation code by default. Agents should not generate large implementation slices unless explicitly asked.

## Go Standards

Go code must follow idiomatic Go, Effective Go, and the Go Proverbs. Prefer the standard library unless a dependency clearly earns its weight.

Required practices:

- Use one binary named `routeup` until a separate binary has a concrete reason to exist.
- Put `context.Context` first on I/O, network, tunnel, proxy, server, and long-running functions.
- Wrap errors with operation context using `fmt.Errorf("doing x: %w", err)`.
- Use `errors.Is` and `errors.As` for error classification.
- Use `log/slog` outside CLI output paths.
- Keep package names short, lowercase, and meaningful.
- Keep interfaces small and define them at the consumer boundary when useful.
- Avoid package-level mutable state unless it is a deliberate singleton with tests.
- Avoid panics except for impossible programmer errors during startup.
- Avoid goroutine leaks; every goroutine must have a cancellation or shutdown path.
- Use `time.Time` and `time.Duration` instead of integer timestamps or duration units.
- Use `net/netip` for IP parsing and storage when practical.
- Use `gofmt` and `go test ./...` as baseline verification once code exists.
- Split files by responsibility when a file grows past roughly 300 lines or combines distinct concerns.
- Minimize exported surface. Exported symbols are public contracts.
- Group related parameters into a named struct when the parameter set represents a logical unit.

Avoid:

- Overbroad interfaces such as `Manager`, `Service`, or `Client` without clear responsibility.
- Unwrapped errors returned from lower-level operations.
- Ignored errors except in explicitly safe cleanup paths. Use `_ = expr // reason` when truly non-fatal.
- Hidden background work without lifecycle ownership.
- Reflection unless it is the simplest correct solution.
- Generics unless they materially simplify a typed collection or algorithm.
- `else` after a `return`; flatten to sequential `if` statements or use default-then-override.
- Inline complex boolean expressions; extract named booleans before the condition.

## CLI Standards

The CLI is the primary product surface.

Required practices:

- Use `cobra` for the command tree.
- Keep command output direct and useful.
- Use stdout for normal output and stderr for diagnostics/errors.
- Every command must have useful `--help` text.
- Prefer flags that describe user intent, such as `--port`, `--server`, `--token`, and `--local-only`.
- Do not expose implementation lifecycle commands in normal docs, such as `proxy start` or `proxy stop`.

Avoid:

- Interactive prompts in commands that must work in scripts.
- Hidden network calls in commands that appear read-only.
- Ambiguous command forms where child process flags can be confused with `routeup` flags.

## Config Standards

Config should stay minimal until real usage proves otherwise.

Required practices:

- Support inference before requiring config.
- Prefer `routeup.json` or a `package.json` `routeup` block only when inference is insufficient.
- Flags override env vars. Env vars override config files. Config files override inference.
- Validate config at load time and return actionable errors.
- Keep route naming as one concept. Avoid forcing users to separately understand project, namespace, and service.

Avoid:

- Pulling in a large config framework before the config model settles.
- Supporting many equivalent config shapes for the same behavior.
- Adding aliases for every command or field before users ask for them.

## Networking Standards

`routeup` is a networking tool. Correctness and lifecycle handling matter more than clever implementation.

Required practices:

- Use the Go standard `net/http` stack unless there is a concrete reason not to.
- Preserve request method, path, query, headers, and body semantics through the proxy/tunnel.
- Support cancellation when clients disconnect.
- Set explicit timeouts for servers, clients, and tunnel operations.
- Handle WebSocket upgrades and streaming responses deliberately, not accidentally.
- Do not buffer unbounded request or response bodies.
- Treat Host routing as security-sensitive input.
- Make route conflicts explicit and easy to diagnose.

Avoid:

- Fire-and-forget goroutines.
- Global default HTTP clients in package internals.
- Silent fallback from HTTPS to HTTP.
- Logging request bodies or sensitive headers by default.

## Security Standards

Security-sensitive code includes token handling, tunnel authentication, TLS setup, route claims, local privileged setup, and logs.

Required practices:

- Tokens must be treated as secrets and never printed after creation.
- Tokens authorize route claims by explicit host patterns.
- The public server must reject route claims outside token scope.
- Route ownership conflicts must fail closed.
- Logs must not capture request bodies, cookies, authorization headers, or webhook signatures by default.
- Local setup must explain privileged changes before making them.
- Secrets in state files must use restrictive file permissions.

Avoid:

- OAuth, user accounts, team management, and billing in v1.
- Storing plaintext secrets in world-readable files.
- Trusting route names or Host headers without validation.

## Error Handling

Required practices:

- Return errors with enough context to identify the failed operation.
- Log errors at the boundary where they are handled, not at every layer.
- Follow the single-handling rule: log or return, not both.
- Error strings should be lowercase and should not end with punctuation.
- Translate internal errors into clear CLI messages.

Examples:

```go
return fmt.Errorf("loading routeup config: %w", err)
```

```go
if errors.Is(err, ErrRouteNotFound) {
    // handle expected route miss
}
```

## Concurrency Standards

Required practices:

- Every goroutine must have an owner and a shutdown path.
- Use contexts for request, tunnel, child-process, and server lifecycles.
- Protect maps with a mutex or keep them single-threaded behind an event loop.
- Prefer simple synchronization over clever channel choreography.
- Run race tests for packages that own concurrent state once they exist.

Avoid:

- Nil channels.
- Unbounded goroutine creation per request.
- Shared mutable maps without synchronization.

## Testing Standards

Baseline commands once code exists:

```bash
go test ./...
```

Expected test coverage by area:

- Route name parsing and hostname mapping: table tests.
- Config discovery and precedence: table tests with temporary directories.
- Registry behavior: unit tests, including conflicts and unregister behavior.
- Proxy behavior: `httptest` integration tests.
- Tunnel behavior: integration tests with in-process server/client where practical.
- CLI behavior: command tests that capture stdout/stderr.
- Setup code: unit-test pure planning functions; keep OS mutation behind small boundaries.

Tests should not require public DNS, real certificates, sudo, or a live VPS unless explicitly marked as manual/integration tests.

## Verification Standards

Each implementation slice must report:

- Test command and result.
- Manual verification path if behavior is user-visible.
- Any skipped tests and why they were skipped.
- Any known limitations left for a later milestone.

## Documentation Standards

Required docs:

- `README.md` explains what the project is for and the intended UX.
- `PLAN.md` records product and architecture decisions.
- `docs/ARCHITECTURE.md` explains the system and request flows.
- `docs/MILESTONES.md` defines the implementation path.
- `docs/ENGINEERING-STANDARDS.md` defines code quality rules.
- `AGENTS.md` tells agents how to work in this repo.

Exported Go packages, types, and functions must have godoc comments once implementation begins.
