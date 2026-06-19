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
dry-run expose output
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
routeup serve api.myapp --port 8080 --dry-run
```

Expected shape:

```txt
route: api.myapp
local: https://api.myapp.localhost
public: https://api.myapp.routeup.dev
target: http://localhost:8080
```

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

## Phase 4: Process Runner

Goal: get Portless-style script-runner usage working locally — `routeup` wraps your dev server and gives it a stable local route.

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
TLS setup
public exposure
path proxy config
```

## Phase 5: Real Local Setup

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
Windows support
```

## Phase 6: Public Server And Tokens

Goal: authenticate clients and reserve public routes.

Build:

```txt
routeup server --domain routeup.dev
token creation (sk_routeup_<random>, Argon2id-hashed in SQLite)
token storage, list, revoke
route claim API
token allow pattern matching
public namespace handling (opt-in via server config, session-only claims)
reserved subdomain enforcement
route conflict handling
```

No tunnel yet. This phase is only route claim control.

Acceptance:

```bash
routeup server --domain routeup.dev --public-namespace try
routeup token create mukul --allow "*.routeup.dev"
ROUTEUP_TOKEN=... routeup expose api.myapp --port 8080 --dry-run-public
routeup expose --port 8080 --dry-run-public  # no token, lands in try.routeup.dev
```

The server should:

- accept token claims whose allow patterns match the requested host
- reject token claims outside the allow pattern with a 403
- accept token-less claims into the public namespace when enabled
- reject token-less claims outside the public namespace with a 401
- treat public-namespace claims as session-only with no grace window
- refuse claims for any reserved subdomain

Do not build yet:

```txt
request forwarding
WebSocket tunnel
public TLS automation
accounts or OAuth
```

## Phase 7: Tunnel MVP

Goal: one public HTTP request reaches a local port.

Build:

```txt
WebSocket tunnel connection
yamux stream multiplexing
public request forwarding
client stream handler
basic request timeout
basic cancellation on disconnect
```

Acceptance:

```bash
routeup server --domain routeup.dev
routeup expose api.myapp --port 8080
curl https://api.myapp.routeup.dev
```

The response should come from the local service on port `8080`.

Do not build yet:

```txt
WebSocket upgrades
SSE hardening
large body tuning
request replay
```

## Phase 8: Streaming, WebSockets, And SSE

Goal: real dev servers work through the tunnel.

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

## Phase 9: Path Proxy — Frontend + API Behind One Route

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

## Phase 10: Route Logs

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

## Phase 11: Inspect And Replay

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

This keeps the project understandable and the implementation tractable, one usable slice at a time.
