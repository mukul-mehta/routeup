---
name: routeup
description: Install, configure, and use routeup for stable local HTTPS routes, public development tunnels, webhook and OAuth callbacks, and browser-agent access to local services. Use when the user mentions routeup, wants to replace localhost ports with named HTTPS URLs, needs a development service reachable from another machine, or wants to inspect local or public HTTP traffic.
license: MIT
compatibility: Requires macOS or Linux. The routeup CLI can be installed by this workflow when it is not already present.
metadata:
  author: mukul-mehta
  source: https://github.com/mukul-mehta/routeup
---

# Routeup

Use routeup to give a local service a stable, trusted HTTPS name such as
`https://myapp.localhost`. Expose that route publicly only when an off-machine
browser, webhook provider, OAuth provider, or collaborator must reach it.

## Operating rules

1. Run routeup commands from the project directory. Config discovery only checks
   the current directory.
2. Check for `routeup.json` and a `routeup` block in `package.json` before
   proposing configuration. `routeup.json` wins when both exist.
3. Prefer a local `.localhost` route. Do not expose a service publicly unless the
   user requests or approves public access.
4. A direct request to install or set up routeup is approval to perform those
   steps. Otherwise, explain that setup changes the machine trust store and
   privileged port handling, then ask before running it.
5. Never print, commit, or place a routeup token in project configuration. Use
   `ROUTEUP_TOKEN` or the private client config written by `routeup setup`.
6. Capture is opt-in and may retain headers and bodies in agent memory. Enable it
   only when requested and redact authorization, cookie, and signature headers.
7. Prefer `--json` for agent-consumed status and one-shot readiness output. Do
   not parse styled human output when structured output is available.
8. Use `routeup <command> --help` as the authoritative reference for the
   installed version if these instructions and the CLI differ.

## Install and set up

First check whether routeup is already installed:

```bash
command -v routeup
routeup version
```

Routeup supports macOS and Linux on arm64 and amd64. It does not currently
support Windows.

If it is missing, prefer Homebrew when Homebrew is available:

```bash
brew install mukul-mehta/tap/routeup
```

Otherwise use the official installer:

```bash
curl -fsSL https://get.routeup.dev | sh
```

The installer may place the binary in `~/.local/bin`; ensure that directory is
on `PATH` before continuing.

Set up trusted local HTTPS once per machine:

```bash
routeup setup
```

Setup creates and trusts a local CA and configures port 443. It may require the
user to approve Touch ID or enter an administrator password. Do not attempt to
bypass that prompt. If the execution environment cannot handle an interactive
privilege prompt, ask the user to run the command in their terminal. For an
explicitly unprivileged setup, use `routeup setup --no-bind --port 8443` and
expect local URLs to include that port.

Verify setup before configuring the project:

```bash
routeup doctor --json
```

Resolve failures reported by `doctor`; do not hide them with insecure TLS flags.

## Inspect the project

Read any existing config, then inspect routeup's resolved non-secret settings:

```bash
routeup config --json
```

Route names are literal when passed explicitly. Without an argument, routeup
uses `ROUTEUP_NAME`, config `name`, then the current directory basename.

See [configuration patterns](references/configuration.md) before creating or
changing project configuration.

## Choose the correct workflow

### Existing process on a known port

The local agent starts automatically on the first `routeup serve` or `routeup
expose` call; there is no separate daemon start command.

Keep the application in its existing terminal and run:

```bash
routeup serve myapp --port 3000
```

For agent-controlled processes, use `--json` to receive a single JSON ready
event on stdout once the route is usable, with no subsequent log rows mixed in:

```bash
routeup serve myapp --port 3000 --json
```

This owns `https://myapp.localhost` until interrupted. To intentionally leave
the route running after the command returns:

```bash
routeup serve myapp --port 3000 --detach
routeup stop myapp
```

Detached serve does not start or stop the application listening on port 3000.

### Configured application runner

When `routeup.json` has `command`, or the `package.json` routeup block has
`script`, run bare routeup:

```bash
routeup
```

Routeup assigns a port, injects `PORT`, `HOST`, `ROUTEUP_LOCAL_URL`, and
`ROUTEUP_URL`, starts the configured command, waits for readiness, and owns its
process group. The child must bind the supplied `HOST` and `PORT`.

Use `routeup exec` for a supporting worker that needs the project URLs and CA
environment but must not claim the route. With no arguments it runs the
configured `script` or `command` from config; an explicit command after `--`
overrides it:

```bash
routeup exec                    # runs configured script/command
routeup exec -- <command> [args...]
```

### Public access

For one owner of both the local route and public tunnel:

```bash
routeup serve myapp --port 3000 --expose
```

To expose an already active route from a second terminal:

```bash
routeup expose myapp
```

Standalone `expose` reuses the active route's targets when one is registered.
If no local route is active for that name, supply an explicit port:

```bash
routeup expose myapp --port 3000
```

Standalone `expose` remains attached until interrupted. `routeup stop` does not
stop it. Use `--random` only when the user wants a throwaway public name.

Public access requires a configured server. If none is configured, ask which
server to use; never invent a server URL or token. Configure credentials without
putting secrets into the project:

```bash
routeup setup --server https://edge.routeup.dev --token "$ROUTEUP_TOKEN"
```

Tokenless claims are possible only when that server enables a public namespace;
they are session-only and first-come-first-served.

Use a local route for browsers on the same machine. Remote and cloud browsers
need a public route because their `localhost` is not the developer machine.

## Observe and debug

List active routes and inspect request metadata:

```bash
routeup routes --json
routeup logs myapp --json
routeup logs myapp --follow --json
```

`logs --json` emits NDJSON. Other supported `--json` commands generally emit one
JSON document or readiness event.

If capture was explicitly enabled in project config, inspect an individual
request with:

```bash
routeup inspect <request-id> --json
```

Captured data is bounded and in-memory, but it may still contain sensitive
application data. Do not repeat it in summaries unless needed for the task.

## Finish cleanly

Foreground routeup commands own their route, tunnel, or child lifecycle and
should be interrupted normally when the task ends. Stop a detached serve with
`routeup stop <name>`. Do not run `routeup uninstall` as cleanup; uninstalling
removes the trusted CA, privileged-port setup, and all routeup state.
