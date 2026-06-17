# routeup Plan

`routeup` gives local services stable HTTPS names and can expose those same routes publicly when needed.

Local development should feel native — stable hostnames, no port juggling — and exposure should feel like a named tunnel. The core primitive is neither a port nor a tunnel. The primitive is a route.

## Product Shape

Every service gets a dotted route name:

```txt
myapp
api.myapp
docs.myapp
```

Routes map directly to hostnames:

```txt
local:   https://myapp.localhost
public:  https://myapp.routeup.dev

local:   https://api.myapp.localhost
public:  https://api.myapp.routeup.dev
```

`.localhost` is the local TLD because RFC 6761 reserves it and modern browsers and resolvers short-circuit it to `127.0.0.1` without any DNS plumbing. `routeup.dev` is the public domain. Note that `.dev` is HSTS-preloaded by Chromium, so public hostnames are HTTPS-only by design — there is no HTTP fallback for the public side, which is the desired behavior.

For OAuth, webhooks, mobile testing, and agent/browser testing, `routeup` should support the one-path model:

```txt
On the developer machine:
myapp.routeup.dev -> local routeup agent -> local service

Outside the developer machine:
myapp.routeup.dev -> public routeup server -> tunnel -> local service
```

That gives one stable URL for browser traffic, callbacks, and public webhooks.

For local-only use, `routeup` never contacts a server and never needs a token. The local agent serves `*.localhost` routes from `routeup setup` alone. The server and token only enter the picture when you run `routeup expose`.

Name resolution rule:

```txt
Any argument containing a dot is taken literally:
  routeup expose api.myapp    -> route api.myapp
  routeup expose api.other      -> route api.other (no myapp scope)

A bare name (no dots) is prefixed with the project name from config:
  project = myapp
  routeup expose                -> route myapp
  routeup expose api            -> route api.myapp

If no project is detected in scope, a bare name is used as-is:
  routeup expose foo            -> route foo
```

## User Experience

The normal commands should be small:

```bash
routeup
routeup expose
routeup expose --port 8080
routeup expose api --port 8080
routeup expose api.myapp --port 9080
routeup status
routeup routes
routeup logs
routeup doctor
routeup setup
routeup update
routeup uninstall
```

Operator-only commands:

```bash
routeup server --domain routeup.dev
routeup token create mukul --allow "*.routeup.dev"
routeup token list
routeup token revoke <token-id>
```

These are hidden from the default `routeup --help` output. How they surface to operators (cobra hidden commands, a `routeup help operator` subcommand, or a build tag) is a Phase 1 implementation choice.

Commands to avoid in normal usage:

```bash
routeup proxy start
routeup proxy stop
routeup pair
routeup login
routeup edge serve
```

The proxy, agent, tunnel, and server are implementation details. Users should think in terms of routes and exposure.

## Setup Model

There should be one setup command:

```bash
routeup setup
```

Local-only setup should prepare:

```txt
trusted local CA
local HTTPS certificates
local agent autostart or auto-start support
port 443 handling
```

Public exposure with a token uses the same setup command:

```bash
routeup setup --server https://routeup.dev --token sk_routeup_xxx
```

Environment-driven usage should also work:

```bash
ROUTEUP_SERVER=https://routeup.dev ROUTEUP_TOKEN=sk_routeup_xxx routeup expose --port 8080
```

The token is optional. Two flows do not need one:

```txt
local-only:        routeup setup + routeup expose --local-only
                   -> https://<name>.localhost
                   no server contact, no token

public namespace:  routeup expose with --server but no --token
                   -> https://<random>.try.routeup.dev (when the server enables it)
                   ephemeral, released on disconnect
```

The token is only required for persistent, scoped claims outside the server's public namespace (see Public Server below).

Do not add a separate `routeup login` or `routeup pair` command for v1. The auth model is: this client has a server token, or it doesn't.

## Config

Two sources are supported:

- `routeup.json` at any directory level.
- A `routeup` block inside `package.json`.

Discovery walks upward from the current working directory. The closest config wins. When both `routeup.json` and a `package.json` `routeup` block exist in the same directory, `routeup.json` takes precedence.

A typical service config:

```json
{
  "name": "myapp",
  "port": 8080
}
```

Or inside `package.json`:

```json
{
  "name": "myapp-web",
  "routeup": {
    "name": "myapp",
    "port": 8080
  }
}
```

An optional repo-root `routeup.json` may carry shared settings (public suffix, token reference) without confusing the model — closest-wins means service-level config still overrides repo-level. There is no separate "project" concept; the `name` field on the closest config is the project name used for bare-name resolution.

## Exposure Model

`routeup expose` means make the route public.

Default exposure is all paths:

```bash
routeup expose --port 8080
```

What you get back depends on token state and the server's public-namespace setting:

```txt
token with --allow "*.alice.routeup.dev":
  routeup expose --port 8080            -> https://<project>.alice.routeup.dev  (named, persistent)
  routeup expose foo --port 8080        -> https://foo.alice.routeup.dev        (named, persistent)
  routeup expose --random --port 8080   -> https://<random>.alice.routeup.dev   (random, persistent within session)

no token, server has public_namespace=try:
  routeup expose --port 8080            -> https://<random>.try.routeup.dev    (random, session-only)
  routeup expose foo --port 8080        -> https://foo.try.routeup.dev         (first-come-first-served, session-only)

no token, server has no public namespace:
  routeup expose --port 8080            -> error: no token and server allows no anonymous claims
```

Expected output (token holder):

```txt
Local:  https://myapp.localhost
Public: https://myapp.alice.routeup.dev
Expose: all paths
```

`--random` is the explicit override for "I have a config name but want a throwaway URL for this run." Without `--random`, the route name comes from config or the CLI argument and follows the resolution rule in Product Shape.

Path-limited exposure can come from config later:

```json
{
  "routeup": {
    "name": "myapp",
    "expose": {
      "paths": ["/api/webhooks/*"]
    }
  }
}
```

That should be an opt-in constraint, not the default behavior.

## Architecture Decision

Use Go.

Reasons:

```txt
single binary
excellent net/http and TLS support
good local daemon and server ergonomics
simple cross-platform distribution path
fast enough for proxy/tunnel workloads
easier to iterate than Rust for this project
```

Build one binary named `routeup`. It should run in several modes:

```txt
CLI mode
local agent mode
public server mode
tunnel client mode
```

Confirmed library choices:

```txt
CLI:                 github.com/spf13/cobra
HTTP / proxy / TLS:  Go standard library
WebSocket:           github.com/coder/websocket
Stream multiplexing: github.com/hashicorp/yamux
Server persistence:  modernc.org/sqlite (pure Go, no cgo)
SQL access:          database/sql (no sqlc until query count grows)
Logging:             log/slog
Token hashing:       golang.org/x/crypto/argon2 (Argon2id)
```

Avoid `viper` at first. Config needs are still unsettled, so a small explicit config loader is better than a large config framework.

The local agent has no persistent storage. Route registry stays in-memory. Logs are an in-memory ring buffer of the last 10k entries; disk-backed log storage is out of scope for v1.

The public server uses SQLite for token storage, claim tracking, and grace-window state. `modernc.org/sqlite` is chosen specifically so the server cross-compiles cleanly without a cgo toolchain. Add `sqlc` only when query count or scan complexity actually hurts.

## Code Structure

Start with this layout once implementation begins:

```txt
cmd/routeup/main.go

internal/cli/
  root.go
  expose.go
  setup.go
  server.go
  token.go
  routes.go
  logs.go
  doctor.go

internal/config/
  config.go
  packagejson.go
  discovery.go

internal/route/
  name.go
  route.go
  matcher.go

internal/agent/
  agent.go
  api.go
  registry.go

internal/proxy/
  local.go
  director.go

internal/process/
  runner.go
  env.go

internal/server/
  server.go
  tokens.go
  claims.go

internal/tunnel/
  client.go
  server.go
  protocol.go

internal/logs/
  entry.go
  store.go
  stream.go

internal/certs/
  ca.go
  cert.go
  trust_darwin.go
  trust_linux.go

internal/setup/
  setup.go
  dns.go
  service.go

internal/state/
  paths.go
  files.go
```

Keep `internal/route` small and central. Route names are the core domain object.

Avoid generic packages like `utils`, `common`, or `helpers`.

## Local Agent

The local agent is an implementation detail, but it is the route brain.

Responsibilities:

```txt
listen on local HTTP/HTTPS ingress
hold active route registry
terminate local TLS
reverse proxy to local targets
record local and public request logs
serve local status and error pages
coordinate active exposes with the public server
```

The CLI should talk to the agent over a local socket. If the agent is not running, commands should attempt to start it automatically. Users should not need `routeup proxy start`.

Lifecycle ownership:

```txt
The agent owns connections     tunnels, child processes, log retention
The foreground CLI owns claims  active route registrations and exposure
```

When a foreground command exits, the agent releases that command's claims and tears down the matching connections. Other active claims and connections are unaffected. No `proxy start` or `proxy stop` style commands are exposed.

CLI-to-agent IPC:

```txt
Transport:   Unix domain socket per user
Path:        ~/.routeup/agent.sock (default), $XDG_RUNTIME_DIR/routeup/agent.sock on Linux when available
Permissions: 0700 directory, 0600 socket
Wire format: JSON over HTTP/1.1
Auth:        filesystem permissions only
Versioning:  /v1/ URL prefix; GET /version returns agent version
```

## Public Server

The public server receives external traffic and forwards it to connected clients.

DNS:

```txt
routeup.dev   -> server IP
*.routeup.dev -> server IP
```

### Reserved subdomains

The server refuses to claim:

```txt
api, admin, app, dashboard, dev, docs, status, www, try
```

The list lives in server config so an operator can extend it. These names are reserved for the server itself (future operator UI, control endpoints), for common conventions users will assume are taken, and for the public namespace below. The chosen public-namespace subdomain is added to this list automatically.

### Public namespace

The server may designate one subdomain as a **public namespace** that anyone can claim under without a token:

```txt
routeup expose --port 8080
-> https://lively-otter-4f2.try.routeup.dev (random, session-only)

routeup expose foo --port 8080
-> https://foo.try.routeup.dev (first-come-first-served, session-only)
```

All public-namespace claims release on disconnect. There is no grace window, no persistence, and no token. Within the namespace, names are first-come-first-served; if a name is held, the next client gets a `409` or, with `--random`, an automatically-assigned name.

The public namespace is **off by default** on self-hosted servers. Enable via server config:

```txt
public_namespace: try
```

Set to empty to disable. The hosted `routeup.dev` deployment runs with `public_namespace: try`. Self-hosted operators opt in explicitly.

### Tokens

Tokens authorize persistent, scoped claims outside the public namespace.

Shape:

```txt
sk_routeup_<43-char base64url>
```

The `sk_` prefix is the Stripe-style "secret key" convention; SAST tools (gitleaks, trufflehog, GitHub secret scanning) recognize the pattern and flag accidental commits. The random part is 32 bytes from `crypto/rand`, base64url-encoded without padding. The server stores only the Argon2id hash in SQLite; plaintext is shown once at creation and never again.

Operator commands:

```bash
routeup token create mukul --allow "*.routeup.dev"
routeup token create alice --allow "*.alice.routeup.dev"
routeup token list
routeup token revoke <token-id>
```

Each token carries one or more allow patterns. The server rejects claims outside the token's allowed host patterns. There is no per-user prefix enforcement; the allow pattern is the only authority. Tiers fall out of the pattern shape:

```txt
allow: ["*.routeup.dev"]            # admin or co-maintainer (whole suffix)
allow: ["*.alice.routeup.dev"]      # alice gets her own namespace
allow: ["*.team-x.routeup.dev"]     # shared team namespace
```

For v1, token minting is **out-of-band**: a friend asks, the operator runs `routeup token create` on the server, sends the token string back privately. No hosted signup flow, no email verification — that is a v2 concern if the hosted server ever opens to public registration.

### TLS

TLS for the public ingress uses wildcard certificates issued via ACME DNS-01. The operator configures one DNS provider API token; the cert manager handles initial issuance and renewal automatically. DNS provider and cert manager library choices are tracked in `docs/OPEN-QUESTIONS.md` as OQ-014 and OQ-015.

## Logs, Inspect, And Replay

Route logs should be first-class.

Where logs live:

```txt
agent:  canonical record for every local and public request
        in-memory ring buffer, last 10k entries
server: minimal record per public request
        method, path, status, timing, request id
```

The agent holds the authoritative log. The server records just enough to debug routing on the public side and to give the operator a per-route counter. `routeup logs` reads from the agent. If the agent is offline when a public request arrives, the request still completes and is logged by the server with the minimal record, but the canonical entry is lost — acceptable for v1.

Commands:

```bash
routeup logs
routeup logs myapp
routeup logs api.myapp
routeup logs api.myapp --follow
routeup logs api.myapp --public
routeup logs api.myapp --local
routeup logs api.myapp --json
```

Default log line:

```txt
12:41:03 public api.myapp POST /api/webhooks/github 200 38ms req_abc123
12:41:07 local  myapp     GET  /settings             200 12ms req_def456
```

Do not capture headers or bodies by default. Later, opt-in capture can power:

```bash
routeup inspect req_abc123
routeup replay req_abc123
```

Replay is explicitly not v1.

## Project Constraints

```txt
License:        MIT
Telemetry:      none
OS support v1:  macOS, Linux (no Windows)
Public suffix:  configurable per deployment; defaults to routeup.dev for the hosted server
```

`routeup` runs as a single binary, ships under MIT, never phones home, and treats its hosted server as one of several possible deployments rather than a privileged default. Self-hosted servers run the same code, configured with a different suffix and DNS provider.

## Distribution And Lifecycle

`routeup` ships as a single binary. Primary channels:

- Homebrew tap for macOS and Linux: `brew install routeup/tap/routeup`.
- GitHub releases for direct download (tarball + checksums).

Lifecycle commands:

```bash
routeup update     # check for and install a newer release
routeup uninstall  # remove agent, CA, certs, and state dir
```

`routeup update` detects the install channel (Homebrew vs direct binary) and delegates to the appropriate updater. `routeup uninstall` must work even when the binary is being replaced — it tears down the agent process and autostart unit, removes the local CA from the trust store, deletes generated certificates, and removes `~/.routeup/`.

## Non-Goals For V1

Do not build these in v1:

```txt
OAuth access protection
team accounts
billing
hosted SaaS control plane
web UI
worktree routing
request replay
Windows support
complex ACLs
```

Possible later additions:

```txt
basic auth for public routes
request inspector
request replay
webhook signature helpers
route namespaces for shared servers
GUI dashboard
Windows support
```

## Reference Docs

Detailed docs live in:

```txt
docs/ARCHITECTURE.md
docs/ENGINEERING-STANDARDS.md
docs/MILESTONES.md
```

Use `docs/MILESTONES.md` to pick implementation slices. Use `docs/ENGINEERING-STANDARDS.md` for code quality rules.

## Open Questions

Tracked in [docs/OPEN-QUESTIONS.md](docs/OPEN-QUESTIONS.md).
