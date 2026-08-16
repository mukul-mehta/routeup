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

Out of scope:

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
go run ./cmd/routeup help
go run ./cmd/routeup doctor
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

Background: `routeup setup` installs a root-owned macOS helper plus LaunchDaemon
for port forwarding and, on Linux, a `setcap` capability on the binary. Linux
capabilities are inode-bound and disappear on replacement. Distribution also
needs a clean teardown story since `brew uninstall` knows nothing about the
LaunchDaemon, the trusted CA, or `~/.routeup`.

> Implementation note: setup markers use the initial version 1 format; no
> migration code exists. `doctor` rejects missing or malformed state, and macOS
> validates the installed helper's binary, IPv4 and IPv6 listeners, and upstream
> target. `ROUTEUP_STATE_DIR` and the `install-devel` Just recipe provide a
> separate high-port profile with its own independently trusted development CA.

Build:

```txt
root-owned LaunchDaemon helper copy under /Library/PrivilegedHelperTools
setup marker records the configured binary path
routeup uninstall (stop agent, remove forwarder/setcap, untrust CA, delete state)
routeup update (delegate to Homebrew or replace a direct-install binary)
doctor port-binding check (missing forwarder on macOS, lost setcap on Linux)
update refreshes the root-owned macOS helper or Linux capability
Homebrew cask (binary + caveat to run `routeup setup`)
```

Acceptance:

```bash
routeup setup
brew upgrade routeup        # forwarder keeps serving; doctor requests refresh
routeup doctor              # flags a lost setcap on Linux after upgrade
routeup uninstall           # removes forwarder, untrusts CA, deletes ~/.routeup
```

The forwarder on macOS executes a root-owned helper copy, not a user-writable
Homebrew or development binary. `routeup update` refreshes that copy; after a
manual package-manager upgrade, `doctor` detects a binary mismatch and asks for
`routeup setup`. On Linux the capability is on the inode, so an upgrade drops
it; the updater reapplies it and `doctor` detects any remaining mismatch via
`getcap`.

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
request inspect
```

## Phase 8.5: Runner Exposure

Goal: make runner mode optionally own public exposure.

> Implementation note: complete. `expose.enabled` is implemented:
> `routeup serve` honors it without requiring `--expose`; bare `routeup`
> contacts the configured server and claims a public route before child launch,
> injects the granted URL into `ROUTEUP_URL` (while `ROUTEUP_LOCAL_URL` stays
> local), and releases the tunnel alongside the route and child group on exit.
> Foreground desired-state reconciliation restores the local claim and public
> exposure after agent restarts or terminal tunnel failures. Framework-specific
> command adapters are intentionally out of scope.

Build:

```txt
explicit expose.enabled config opt-in for runner mode
existing server/token precedence reused by the runner
public route granted before child launch
ROUTEUP_LOCAL_URL remains local
ROUTEUP_URL contains the granted public URL when exposure is enabled
one runner process owns local registration, public exposure, and child cleanup
```

Example config:

```json
{
  "name": "myapp",
  "command": "go run ./cmd/dev",
  "expose": {
    "enabled": true
  }
}
```

Acceptance:

```bash
routeup
```

With exposure disabled, both route URL variables remain local and no server is
contacted. With `expose.enabled`, the runner obtains a public route before child
launch, injects that route into `ROUTEUP_URL`, serves both URLs, and releases the
tunnel, route, and complete child process group together on exit.

Do not build yet:

```txt
generic shell-command rewriting or an adapter plugin system
package-manager lifecycle hook emulation
framework-specific command adapters
```

## Phase 9: Route Logs

Goal: make local and public traffic visible.

> Implementation note: complete. The agent owns one 1,024-entry in-memory ring
> for local and public completed request metadata. `routeup logs [route]` reads
> the JSON agent API, and `--follow` consumes its SSE stream. The proxy records
> the matched target, status, and duration without buffering request or response
> bodies; request capture and inspect are implemented in Phase 10 below.
> `--follow` always streams plain lines; `--json` emits NDJSON. The shared
> Lip Gloss theme is also used by existing human-readable status commands.

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
routeup logs --since/--method/--status/--limit
1024-entry in-memory request ring
```

Acceptance:

```bash
routeup logs api.myapp --follow --public
```

Shows incoming webhook traffic in real time.

Do not build yet:

```txt
inspect
```

## Phase 10: Request Capture And Inspect

Goal: make webhook debugging excellent.

> Implementation note: complete. Per-direction `capture.request` and
> `capture.response` settings are loaded from either config source, propagated
> through local claims and standalone/public exposure, and
> applied by both the local and tunnel-side target handlers. The handler wraps
> the request body so forwarding semantics are unchanged while retaining a
> bounded copy of headers and body. Capture starts only after public-path and
> target matching; blocked or unmatched requests receive metadata logs without
> retained exchange data. `GET /v1/requests/{id}` serves the complete entry over
> the agent's Unix socket, and `routeup inspect` safely formats it for the user.
> `--raw` exposes unescaped retained values and `--json` emits the full exchange.
> The 256 KiB per-direction limit includes headers and body; oversized or
> incomplete bodies are returned as a retained prefix with `Complete: false`.

Build:

```txt
opt-in capture.request and capture.response config
capture.redact_headers config for captured headers
request and response header/body capture
256 KiB per-direction capture limit
routeup inspect <request-id>
routeup inspect --raw/--json
```

Acceptance:

```bash
routeup inspect req_Ap7kQ3mN8vR2xLzC
```

Capture is disabled by default. Request and response retention are enabled
independently in `routeup.json` or the `package.json` `routeup` block. Retained
data remains only in the agent's 1024-entry in-memory ring and is limited to 256
KiB per direction including headers and body. `routeup inspect` escapes retained
bytes by default.

## Phase 11: Public Server Rate Limiting

Goal: protect hosted and anonymous ingress from abuse while preserving a simple
self-hosted mode.

Build:

```txt
configurable in-memory token-bucket limits and bursts
claim limits by authenticated token identity
global anonymous/public-namespace claim limits
request limits by active public host
bounded idle-key eviction
429 responses with Retry-After
low-cardinality rejection metrics
zero values disable each limit for self-hosted operators
```

Per-source-IP fairness requires trustworthy client identity through Fly's raw
TCP path and is not part of the first slice. Distributed persistence, billing
quotas, and WAF behavior are out of scope.

## Phase 12: Public Route Protection

Goal: add optional authentication to public preview routes.

Build:

```txt
per-exposure HTTP Basic Auth
interactive/environment-based secret input, never plaintext project config
constant-time credential verification
401 challenge before the request reaches the local app
protection state retained by exposure reconciliation
no credentials in logs, status, metrics, SQLite, or agent responses
```

OAuth, SSO, accounts, teams, and path-level ACLs are out of scope.

## Phase 13: Request Replay

Goal: replay one captured request against its currently active local route.

Build:

```txt
routeup replay <request-id>
agent replay endpoint backed by retained capture
preserve method, path/query, body, and safe end-to-end headers
omit hop-by-hop and sensitive headers by default
require complete capture and an active route
report replay status and new request ID
```

Scheduled, bulk, edited, and public-endpoint replay are out of scope.

## Phase 14: Project Initialization

Goal: safely create a minimal valid project configuration.

Build:

```txt
routeup init with scriptable flags and TTY prompts
routeup.json by default; package.json block only when explicitly selected
name, port/targets, command, and package script inputs
existing loader validation and atomic writes
refuse to overwrite either existing config source
```

Framework detection, command rewriting, adapters, and monorepo walk-up are out
of scope.

## Phase 15: Local Dashboard

Goal: provide one interactive local observability surface.

> First TUI slice complete. `routeup dashboard` is a read-only Bubble Tea view
> of active routes, all managed public exposures, tunnel state, live requests,
> and safely rendered opted-in captures. It reuses the existing agent APIs and
> terminal theme, remains open while the agent is offline, and never starts or
> mutates the agent. The browser dashboard is deferred until after the initial
> release; no `--web` flag or loopback HTTP surface exists yet.

Build:

```txt
routeup dashboard opens a full-screen TUI by default
active routes, exposures, tunnel state, live logs, and inspect details
reuse the shared terminal theme and live-log model
read-only first slice; replay and mutations arrive with their owning milestones
```

Later web slice:

```txt
routeup dashboard --web opens an embedded browser dashboard
shared agent client/model between TUI and web views
embedded assets with no CDN or telemetry
```

The web dashboard is loopback-only at an internal route such as
`https://dashboard.routeup.localhost`, with strict Host/Origin checks and a
random session token. It must never be registered as a user route or exposed
publicly. Accounts, server administration, persistence, and hosted dashboards
are out of scope.

## Phase 15.5: Dashboard Tabs And JSON Inspection

Goal: turn the read-only dashboard into a focused multi-view debugging surface.

Build:

```txt
visible tab bar: Overview, Routes, Requests, Config
Tab and Shift-Tab cycle tabs; keys 1-4 jump directly
preserve selection and scroll position independently per tab
Overview shows agent health plus route, exposure, and request counts
Routes shows local URLs, targets, public exposure, paths, and tunnel state
Requests keeps the live bounded list, filtering, and captured-request drilldown
Config shows resolved non-secret project settings and their source
request and response details remain separate
valid application/json bodies are pretty-printed with syntax colors
invalid or non-JSON bodies fall back to terminal-safe escaped text
responsive layouts retain controls and selected request IDs on narrow terminals
```

Acceptance:

```txt
keyboard-only navigation reaches every tab and row
live requests continue updating while another tab is selected
JSON formatting never changes retained bytes or emits terminal controls
large JSON and text bodies remain fully reachable through scrolling
offline and agent-restart states remain clear without leaving the dashboard
```

Web UI, replay, route mutation, config editing, persistence, and server
administration remain out of scope for this slice.

## Phase 16: mDNS/LAN Mobile Mode

Goal: explicitly expose one active route to devices on the same LAN without the
public server.

Build:

```txt
routeup mobile <route> as a foreground opt-in session
collision-checked <route>.local mDNS advertisement
listener pinned to one selected private interface and high port
exact .local leaf certificate from the existing local CA
URL, QR code, CA fingerprint, and mobile trust instructions
distinct lan request-log source
advertisement and listener cleanup on exit
```

Android zero-configuration, permanent LAN exposure, internet routing, and LAN
port 443 are out of scope.

## Milestone Discipline

Do not skip from Phase 2 to TLS or tunnels. The hard parts should be isolated:

```txt
route model before networking
high-port local routing before privileged setup
server claims before tunnel forwarding
plain HTTP forwarding before WebSocket/SSE
logs before request capture and inspect
```

Process Runner sits late on purpose: local TLS, tunnels, streaming, and path
routing already work before process orchestration is added. Phase 8 owns the
local process lifecycle; Phase 8.5 composes it with public exposure. Framework
adapters remain out of scope.

This keeps the project understandable and the implementation tractable, one usable slice at a time.
