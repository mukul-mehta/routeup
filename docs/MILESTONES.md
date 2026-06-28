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
.github/workflows/ci.yml (just test + just lint on push and PR)
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
package.json name discovery
routeup config discovery
flag/env/config/inference precedence
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

Build macOS and Linux together. Trust stores and port-443 strategies differ between them; both must work before this phase is done. See `docs/OPEN-QUESTIONS.md` OQ-002 and OQ-003 for the per-platform port-443 questions.

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
doctor port-binding check (missing forwarder on macOS, lost setcap on Linux)
Homebrew formula (binary + caveat to run `routeup setup`)
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
auto-update (routeup update)
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
> multi-label names are rejected, reserved names protect only the root tier, and
> granting a namespace reserves its label at root. Claims are asserted over the
> tunnel control channel; there is no separate HTTP claim API. The server serves
> HTTPS: `--tls-mode acme` (default) auto-issues wildcards via Let's Encrypt +
> Cloudflare DNS-01 (`certmagic`): `*.<domain>` and `*.try.<domain>` at startup,
> and `*.<namespace>.<domain>` on first claim. `--tls-mode cert` serves an
> operator-provided cert. Real public DNS
> and a deployed host are the remaining deployment step. Acceptance needs a
> `--server`/`ROUTEUP_SERVER` pointing at the running server.

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
curl https://api.myapp.routeup.dev
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
> excluded from the default suite/CI (need node + npm + network) and Skip when
> node is absent.


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

## Phase 8: Process Runner

Goal: get Portless-style script-runner usage working — `routeup` wraps your dev server and gives it a stable local route, with the env vars pointing at the now-real local and public URLs from earlier phases.

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
    "dev:app": "vite"
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
ROUTEUP_URL=https://myapp.routeup.dev
ROUTEUP_LOCAL_URL=https://myapp.localhost
```

Do not build yet:

```txt
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
request IDs
routeup logs
routeup logs --follow
routeup logs --public
routeup logs --local
routeup logs --json
bounded retention
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
opt-in header capture
opt-in body capture
routeup inspect <request-id>
routeup replay <request-id>
redaction controls
retention controls
```

Acceptance:

```bash
routeup inspect req_abc123
routeup replay req_abc123
```

Replay should show exactly what will be replayed and require captured bodies.

## Milestone Discipline

Do not skip from Phase 2 to TLS or tunnels. The hard parts should be isolated:

```txt
route model before networking
high-port local routing before privileged setup
server claims before tunnel forwarding
plain HTTP forwarding before WebSocket/SSE
logs before inspect/replay
```

Process Runner sits late on purpose: once TLS, public exposure, streaming, and path proxy are in, `ROUTEUP_URL` and `ROUTEUP_LOCAL_URL` are real working URLs the child can use without caveats, and the runner is just convenience over a complete routing stack.

This keeps the project understandable and the implementation tractable, one usable slice at a time.
