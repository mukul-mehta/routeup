# Milestones

This document defines how to build `routeup` one slice at a time.

## How To Pick Up A Milestone

1. Read `PLAN.md`, `docs/ARCHITECTURE.md`, and `docs/ENGINEERING-STANDARDS.md`.
2. Pick exactly one milestone or one sub-slice from a milestone.
3. Write down the intended behavior before writing code.
4. Add or update tests with the implementation unless the slice is docs-only or OS-manual.
5. Keep the implementation minimal. Do not pull later milestone work forward.
6. Run the relevant verification command.
7. Update docs if command behavior, config behavior, or architecture changed.

Each milestone has acceptance criteria. A milestone is done only when the acceptance criteria pass.

## Phase 0: Documentation

Goal: lock in product, architecture, milestones, engineering standards, and open questions before any code is written.

Build:

```txt
PLAN.md
README.md
LICENSE
AGENTS.md
docs/ARCHITECTURE.md
docs/ENGINEERING-STANDARDS.md
docs/MILESTONES.md
docs/OPEN-QUESTIONS.md
```

Acceptance:

```txt
All planning docs are committed and internally consistent.
README is short and links to the docs.
No Go source exists yet.
```

Do not build yet:

```txt
go.mod
cobra commands
lint config
CI
task runner
```

## Phase 1: Scaffolding And Walking Skeleton

Goal: make the repo buildable with the chosen toolchain, lint pipeline, runner, and CI. Wire an empty cobra CLI that prints `--help` and stubs the user-facing commands. No real networking behavior.

Build:

```txt
go.mod (module github.com/mukul-mehta/routeup, go 1.24)
.gitignore
.editorconfig
.golangci.yml (errcheck, govet, staticcheck, ineffassign, unused, gofmt, goimports)
justfile (test, test-race, lint, fmt, build, run, ci)
initial .github/workflows/ci.yml (test + lint on push and PR)
cmd/routeup/main.go
internal/cli/root.go
internal/cli/root_test.go
version command or --version support
placeholder doctor/routes/logs commands
```

Acceptance:

```bash
just test
just lint
just dev help
just dev doctor
```

CI must be green on push and PR.

Do not build yet:

```txt
TLS
local agent
tunnels
setup mutation
config files
route parser
```

## Phase 2: Route Names And Config Discovery

Goal: lock down the route model before networking complexity enters.

Build:

```txt
route name parser
route name validator
hostname mapping
package.json routeup-block discovery
routeup.json discovery
flag/env/config precedence
```

Core route rules:

```txt
myapp is valid
api.myapp is valid
api..myapp is invalid
.myapp is invalid
myapp. is invalid
routeup.dev suffix is not part of the route name
localhost suffix is not part of the route name
```

Acceptance:

```bash
go test ./internal/route/... ./internal/config/...
```

Route names parse and validate per the rules above, and the name and port
resolve from flag, env, and config in precedence order.

Do not build yet:

```txt
reverse proxy
tunnel
certificates
port 443
```

## Phase 3: Local Agent On A High Port

Goal: prove local routing before dealing with privileged ports or certificates.

Run the local agent on a high port first, for example:

```txt
127.0.0.1:7070
```

Temporary local URL form:

```txt
http://api.myapp.localhost:7070
```

Build:

```txt
local agent process
route registry
CLI-to-agent API
register route
unregister route
reverse proxy by Host header
routeup serve --port 8080
```

Acceptance:

```bash
python3 -m http.server 8080
routeup serve api.myapp --port 8080
curl -H 'Host: api.myapp.localhost' http://127.0.0.1:7070
```

The response should come from the server running on port `8080`.

Do not build yet:

```txt
TLS
setup
public server
tunnel
process runner
```

## Phase 4: Real Local Setup

Goal: remove visible local ports for `.localhost` routes.

Build macOS and Linux together. Trust stores and port-443 strategies differ
between them; both must work before this phase is done. The resolved platform
decisions are recorded in `docs/ARCHITECTURE.md`.

Build:

```txt
routeup setup
local CA creation
local CA trust
certificate generation
local HTTPS listener
port 443 handling
doctor checks for setup state
```

Acceptance:

```bash
routeup setup
routeup serve api.myapp --port 8080
open https://api.myapp.localhost
```

Do not build yet:

```txt
public tunnel
process runner
Windows support
```

## Phase 4.5: Packaging And Lifecycle

Goal: make routeup installable and removable cleanly, and survive binary upgrades.

Background: `routeup setup` installs a macOS LaunchDaemon (the port-443 forwarder) and, on Linux, a `setcap` capability on the binary. Both reference the binary by path/inode, so a package upgrade can break them. Distribution also needs a clean teardown story since `brew uninstall` knows nothing about the LaunchDaemon, the trusted CA, or `~/.routeup`.

Build:

```txt
stable LaunchDaemon binary path (Homebrew opt/bin symlink, survives upgrades)
setup marker records the configured binary path
routeup uninstall (stop agent, remove forwarder/setcap, untrust CA, delete state)
routeup update (delegate to Homebrew or replace a direct-install binary)
doctor port-binding check (missing forwarder on macOS, lost setcap on Linux)
Homebrew cask (binary + caveat to run `routeup setup`)
```

Acceptance:

```bash
routeup setup
brew upgrade routeup        # forwarder still works (plist points at the stable symlink)
routeup doctor              # flags a lost setcap on Linux after upgrade
routeup uninstall           # removes forwarder, untrusts CA, deletes ~/.routeup
```

The forwarder on macOS is unaffected by upgrades because the plist points at the stable Homebrew symlink. On Linux the capability is on the inode, so an upgrade drops it; `doctor` detects this via `getcap` and `routeup setup` reapplies.

Do not build yet:

```txt
Windows packaging
signed/notarized macOS binaries
```

## Phase 5: Public Server, Tokens, And Tunnel

Goal: authenticate clients, reserve public routes, and forward one public HTTPS
request to a local port over a tunnel.

> Implementation note: built and verified end-to-end over loopback (server +
> agent tunnel client + local backend), reached via a `Host` header. The server
> derives the public host from the token's allow pattern (the client sends only
> a route name), so a scoped token cannot claim outside its namespace; an
> out-of-scope route is rejected as the reserved-subtree / out-of-domain 403.
> The agent owns the tunnel client and `routeup expose` holds the claim until
> Ctrl-C. Public hosts are one label under a namespace base (`<label>.<base>`):
> the CLI normalizes dotted local routes to a hyphenated public label, while the
> server rejects raw multi-label claims. Reserved names protect only the root
> tier, and granting a namespace reserves its label at root. Claims are asserted
> over the tunnel control channel; there is no separate HTTP claim API. The
> server serves HTTPS: `--tls-mode acme` (default) auto-issues wildcards via
> Let's Encrypt + Cloudflare DNS-01 (`certmagic`): `*.<domain>` and
> `*.try.<domain>` at startup, and `*.<namespace>.<domain>` on first claim.
> `--tls-mode cert` serves an operator-provided cert. The hosted deployment uses
> `routeup.dev`; acceptance can also run against a loopback/self-hosted server.

Build:

```txt
routeup server --domain routeup.dev
token creation (sk_routeup_<random>, SHA-256-hashed in SQLite)
token storage, list, revoke
token allow pattern matching
public namespace handling (opt-in via server config, session-only claims)
reserved subdomain enforcement
route conflict handling
WebSocket tunnel connection
yamux stream multiplexing
public request forwarding
client stream handler
basic request timeout
basic cancellation on disconnect
```

Acceptance:

```bash
routeup server --domain routeup.dev --public-namespace try
routeup token create mukul --allow "*.routeup.dev"
ROUTEUP_TOKEN=... routeup expose api.myapp --port 8080
curl https://api-myapp.routeup.dev
```

The server should:

- accept token claims whose allow patterns match the requested host
- reject token claims outside the allow pattern with a 403
- accept token-less claims into the public namespace when enabled
- reject token-less claims outside the public namespace with a 401
- treat public-namespace claims as session-only with no grace window
- refuse claims for any reserved subdomain
- forward a public HTTPS request through the tunnel to the local port

The response should come from the local service on the target port.

Do not build yet:

```txt
WebSocket upgrades
SSE hardening
large body tuning
request replay
accounts or OAuth
```

## Phase 6: Streaming, WebSockets, And SSE

Goal: real dev servers work through the tunnel.

> Implementation note: the M5 stdlib-over-yamux tunnel was kept. M6 tuned yamux
> for streaming workloads (`MaxStreamWindowSize=1MiB`,
> `ConnectionWriteTimeout=30s`) and added two layers of tests.
>
> Fast synthetic tests (`go test ./...`, `internal/streamtest`) assert the
> transport-invariant properties a real dev server can't be instrumented to show
> deterministically: a WebSocket upgrade/echo, SSE incremental (non-buffered)
> delivery, large-body integrity, slow-first-byte, and client-disconnect
> cancellation — across the tunnel path (`TunnelRegistry`), the real ingress path
> (`serveIngress`), and the local `.localhost` path (`proxy.New`).
>
> Real end-to-end tests (`just test-integration`,
> `internal/server/integration_test.go`, `//go:build integration`) spin up an
> actual Vite and Next.js dev server, expose each through `serveIngress`, and
> drive its real HMR channel: Vite over a `vite-hmr` WebSocket, Next over its
> `/_next/webpack-hmr` WebSocket (Next switched HMR from SSE to WebSocket in v12).
> Both assert a file edit produces a live HMR push through the tunnel. They are
> excluded from the default Go suite, run in the dedicated integration CI
> workflow, and Skip when node is absent.


Build:

```txt
WebSocket upgrades
SSE streaming
large request and response bodies
request cancellation
response streaming
idle timeouts
backpressure handling
```

Acceptance:

```txt
Vite HMR works
Next dev works
webhook POST bodies work
long-lived SSE does not buffer forever
client disconnect cancels upstream work
```

Do not build yet:

```txt
request body capture
replay
GUI inspection
```

## Phase 7: Path Proxy — Frontend + API Behind One Route

Goal: support frontend and API behind a single route.

> Implementation note: M7 changed the route shape from one port to path-routed
> targets. `routeup.json` and the package.json `routeup` block now accept
> `targets: [{"path":"/","port":5173},{"path":"/api","port":8080}]`; the
> existing `port` field remains shorthand for the root `/` target. The CLI also
> accepts repeatable `--target /path=port` overrides. The agent registry stores
> one owner per route with multiple targets, and both the local `.localhost`
> proxy and agent-side tunnel handler choose the upstream by longest path-prefix
> match. Public exposure can be limited with `expose.paths` (for example
> `["/api/*"]`); blocked public paths return 404 while local routes still serve
> all configured targets. `routeup expose <name>` first reuses an already-
> registered local route's targets when no explicit target override is passed,
> then falls back to config/flags. Public-domain local mirror work is planning-
> only for this milestone; no DNS or resolver behavior was added.

Build:

```txt
path routing
/api -> fixed API target
/ -> dynamic app target
configured project expose
path-limited expose
public-domain local mirror planning
```

Acceptance:

```txt
https://myapp.routeup.dev/      -> dev server (e.g. Vite, Next)
https://myapp.routeup.dev/api/* -> API backend
```

Do not build yet:

```txt
replay
advanced ACLs
team namespaces
```

## Phase 8: Local Process Runner

Goal: get the local Portless-style script-runner flow working. Bare `routeup`
wraps a configured development command, gives it a stable local HTTPS route,
and owns the child and route lifecycle.

> Implementation note: complete, manually verified, and covered by the Linux
> integration workflow. Bare `routeup` resolves package.json `routeup.script`
> or routeup.json `command`, assigns the root target port, injects `PORT`,
> `HOST`, `ROUTEUP_LOCAL_URL`, and a local `ROUTEUP_URL`, and owns the child
> process group and route cleanup. Interactive suspension with Ctrl-Z is not
> supported. In an interactive terminal, the child group is foreground and
> receives Ctrl-C directly; it must respond to SIGINT. A SIGTERM sent directly
> to routeup uses the managed shutdown and escalation path.

Build:

```txt
script discovery
child process runner
PORT/HOST/ROUTEUP_* env injection
route register while child runs
route unregister on exit
signal handling
child process exit-code propagation
```

Example package config:

```json
{
  "scripts": {
    "dev": "routeup",
    "dev:app": "node server.mjs"
  },
  "routeup": {
    "name": "myapp",
    "script": "dev:app"
  }
}
```

Acceptance:

```bash
pnpm dev
```

The app should receive:

```txt
PORT=<assigned-port>
HOST=127.0.0.1
ROUTEUP_LOCAL_URL=https://myapp.localhost
ROUTEUP_URL=https://myapp.localhost
```

The route should become reachable after the child binds its assigned port. When
the child exits after Ctrl-C, or routeup receives SIGTERM, the whole child
process group should stop and the route should be removed. A direct cancellation
of routeup escalates to SIGKILL after five seconds. After readiness, routeup
returns the child's exit status; a child that exits successfully before
listening is treated as a startup failure.

Do not build yet:

```txt
config-driven public runner exposure (Phase 8.5)
framework command adaptation (Phase 8.5)
child stdio capture into agent logs
request inspect
replay
```

## Phase 8.5: Runner Exposure And Framework Adapters

Goal: make runner mode optionally own public exposure and support unchanged
commands for explicitly tested frameworks that do not honor `PORT` or `HOST`.

> Implementation note: planned. Phase 8 users can already run `routeup expose`
> in a second terminal to expose the runner's dynamic target. This milestone
> integrates that lifecycle into bare `routeup` and starts framework adaptation
> with the exact bare `vite` command shape rather than a generic shell rewriter.

Build:

```txt
explicit expose.enabled config opt-in for runner mode
existing server/token precedence reused by the runner
public route granted before child launch
ROUTEUP_LOCAL_URL remains local
ROUTEUP_URL contains the granted public URL when exposure is enabled
one runner process owns local registration, public exposure, and child cleanup
narrow adapter for an unchanged bare vite command
documented behavior for explicit or conflicting framework flags
runner-driven integration coverage for local and exposed lifecycles
```

Example package config:

```json
{
  "scripts": {
    "dev": "routeup",
    "dev:app": "vite"
  },
  "routeup": {
    "name": "myapp",
    "script": "dev:app",
    "expose": {
      "enabled": true
    }
  }
}
```

Acceptance:

```bash
pnpm dev
```

An unchanged `vite` command should bind the routeup-assigned loopback address
and port. With exposure disabled, both route URL variables remain local and no
server is contacted. With `expose.enabled`, the runner should obtain a public
route before child launch, inject that route into `ROUTEUP_URL`, serve both URLs,
and release the tunnel, route, and complete child process group together on
exit. Automated tests may use a loopback public server and must not require
public DNS, production certificates, sudo, or a live VPS.

Do not build yet:

```txt
generic shell-command rewriting or an adapter plugin system
package-manager lifecycle hook emulation
untested adapters for additional frameworks
child stdio capture into agent logs
request inspect
replay
```

## Phase 9: Route Logs

Goal: make local and public traffic visible.

Build:

```txt
access log entries
local/public source field
opaque request IDs (req_<16-char-random>)
routeup logs
routeup logs --follow
routeup logs --public
routeup logs --local
routeup logs --json
1024-entry in-memory request ring
```

Acceptance:

```bash
routeup logs api.myapp --follow --public
```

Shows incoming webhook traffic in real time.

Do not build yet:

```txt
header capture
body capture
inspect
replay
```

## Phase 10: Inspect And Replay

Goal: make webhook debugging excellent.

Build:

```txt
opt-in capture: true config
request and response header/body capture
256 KiB request and response capture limit
routeup inspect <request-id>
routeup replay <request-id>
```

Acceptance:

```bash
routeup inspect req_Ap7kQ3mN8vR2xLzC
routeup replay req_Ap7kQ3mN8vR2xLzC
```

`capture` is disabled by default. When set to `true` in `routeup.json` or the
`package.json` `routeup` block, it captures request and response headers and
bodies for that route. The first capture slice has no configurable retention or
redaction controls; users must opt in only for traffic they are comfortable
retaining in local agent memory.

The agent keeps its last 1024 request records in one in-memory ring. Each
captured request and response message retains at most 256 KiB, including
headers and body. When the ring fills, the oldest record and any capture it
holds are removed together. Partial captures can be inspected but cannot be
replayed.

`routeup inspect` displays the captured original request and response.
`routeup replay` sends the captured request once to its original loopback
target, then prints the result. It fails without sending when capture was
disabled, incomplete, truncated, or evicted.

## Milestone Discipline

Do not skip from Phase 2 to TLS or tunnels. The hard parts should be isolated:

```txt
route model before networking
high-port local routing before privileged setup
server claims before tunnel forwarding
plain HTTP forwarding before WebSocket/SSE
logs before inspect/replay
```

Process Runner sits late on purpose: local TLS, tunnels, streaming, and path
routing already work before process orchestration is added. Phase 8 owns the
local process lifecycle; Phase 8.5 composes it with public exposure and narrowly
supported framework adapters.

This keeps the project understandable and the implementation tractable, one usable slice at a time.
