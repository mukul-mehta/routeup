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

Routes map to hostnames. Locally, a route can be dotted — the local CA mints a
per-SNI leaf for any depth:

```txt
local:   https://myapp.localhost
local:   https://api.myapp.localhost
```

Publicly, a route is exposed as **a single label under a namespace**, because a
publicly-trusted wildcard certificate (`*.<namespace>.routeup.dev`) only covers
one label. There are three public tiers (see Public Server → Public hostname
model):

```txt
public (no token):       https://<label>.try.routeup.dev
public (root token):     https://<label>.routeup.dev
public (namespace token):https://<label>.mukul.routeup.dev
```

A multi-label route name is fine locally. For public exposure, the CLI
normalizes dots to hyphens (`api.myapp` becomes `api-myapp`) because the server
accepts exactly one label under a namespace base. Raw multi-label claims sent
directly to the server are rejected.

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
  routeup serve api.myapp       -> route api.myapp
  routeup serve api.other       -> route api.other (no myapp scope)

A bare name (no dots) is prefixed with the project name from config:
  project = myapp
  routeup serve                 -> route myapp
  routeup serve api             -> route api.myapp

If no project is detected in scope, a bare name is used as-is:
  routeup serve foo             -> route foo
```

## User Experience

The normal commands should be small:

```bash
routeup                                # script-runner / Portless mode
routeup serve                          # serve a local route (default local-only)
routeup exec -- <command>              # project environment without route ownership
routeup serve --port 8080
routeup serve api --port 8080
routeup serve api.myapp --port 9080
routeup serve api.myapp --port 9080 --expose   # also expose publicly
routeup expose api.myapp               # expose active or configured targets publicly
routeup agent status
routeup dashboard
routeup routes
routeup logs
routeup config
routeup doctor
routeup setup
routeup update
routeup uninstall
```

The split: `serve` creates a route (local by default; `--expose` or
`expose.enabled` adds public exposure in one go). `exec` runs one configured or
explicit command with Routeup's project URL and CA environment but never owns a
route or exposure. Standalone `expose` reuses an active route's targets when
available, otherwise it exposes targets from flags or config without creating a
local registration. Bare `routeup` is the Phase 8 local script runner; Phase 8.5
added `expose.enabled` for config-driven exposure before child launch.
Framework-specific command adapters are out of scope.

Operator-only commands:

```bash
routeup server --domain routeup.dev
routeup token create mukul --allow "*.routeup.dev"
routeup token list
routeup token revoke <token-id>
```

These are hidden Cobra commands and do not appear in the default `routeup --help` output.

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
local agent auto-start on demand
port 443 handling
```

Public exposure with a token uses the same setup command:

```bash
routeup setup --server https://edge.routeup.dev --token sk_routeup_xxx
```

Environment-driven usage should also work:

```bash
ROUTEUP_SERVER=https://edge.routeup.dev ROUTEUP_TOKEN=sk_routeup_xxx routeup expose --port 8080
```

Remote server URLs must use HTTPS; plaintext HTTP is accepted only for loopback
testing. A saved token is scoped to its saved server URL and is not carried to a
flag or environment override naming a different server.

The token is optional. Two flows do not need one:

```txt
local-only:        routeup setup + routeup serve
                   -> https://<name>.localhost
                   no server contact, no token

public namespace:  routeup expose --random with --server but no --token
                   -> https://<random>.try.routeup.dev (when the server enables it)
                   ephemeral, released on disconnect
```

The token is only required for persistent, scoped claims outside the server's public namespace (see Public Server below).

Do not add a separate `routeup login` or `routeup pair` command for v1. The auth model is: this client has a server token, or it doesn't.

## Config

Two sources are supported:

- `routeup.json`.
- A `routeup` block inside `package.json`.

Discovery looks in the current working directory only. When both `routeup.json` and a `package.json` `routeup` block exist in that directory, `routeup.json` takes precedence. Multi-directory walk-up is not implemented in v1; it may be added later when monorepo workflows justify it.

Per-language embeds beyond `package.json` (e.g. `pyproject.toml`, `Cargo.toml`) are out of scope for v1 — non-JS projects use `routeup.json` directly. Adding a new embed format pulls in a parser dependency and is a deliberate later decision.

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

Bare runner mode resolves a package script explicitly:

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

Non-JavaScript projects put the shell command in `routeup.json`:

```json
{
  "name": "myapp",
  "command": "go run ."
}
```

`script` is package.json-only and `command` is routeup.json-only. The package
loader resolves the selected script to its command string; the runner executes
that string through `sh -c` and does not run package-manager lifecycle hooks for
the selected child script.

`routeup exec` uses the same configured `script` or `command` when invoked
without arguments. An explicit argv after `--` overrides it. Exec injects the
project's local URL, an active public URL when available, and local CA trust,
but does not start the agent, register a route, expose it, or wait for a target.

For frontend + API behind one route, use path targets:

```json
{
  "name": "myapp",
  "targets": [
    { "path": "/", "port": 5173 },
    { "path": "/api", "port": 8080 }
  ]
}
```

The older `port` field remains shorthand for `{ "path": "/", "port": <port> }`.

There is no separate "project" concept; the `name` field on the config is the project name used for bare-name resolution. Shared server and token settings live in `~/.routeup/client.json`, written by `routeup setup`, not in the per-service file.

For isolated development and integration tests, `ROUTEUP_STATE_DIR` relocates
the complete per-user state root (socket, PID, log, CA, setup marker, and client
configuration) without changing `HOME`. It does not affect project config
discovery. `ROUTEUP_AGENT_SOCKET` remains a socket-only override with higher
precedence.

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
  routeup expose api.myapp --port 8080  -> https://api-myapp.alice.routeup.dev  (named, persistent)
  routeup expose --random --port 8080   -> https://<random>.alice.routeup.dev   (random, persistent within session)

no token, server has public_namespace=try:
  routeup expose --random --port 8080   -> https://<random>.try.routeup.dev    (random, session-only)
  routeup expose api.myapp --port 8080  -> https://api-myapp.try.routeup.dev   (first-come-first-served, session-only)

no token, server has no public namespace:
  routeup expose api.myapp --port 8080  -> error: no token and server allows no anonymous claims
```

Expected output (token holder):

```txt
public: https://myapp.alice.routeup.dev
expose: all paths
```

When standalone `expose` reuses an active local claim, it also prints that
claim's local URL. An explicit/config-only target is public-only and does not
create or advertise a local route.

`--random` is the explicit override for "I have a config name but want a throwaway URL for this run." Without `--random`, the route name comes from config or the CLI argument and follows the resolution rule in Product Shape.

Path-limited exposure comes from config:

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

Phase 8.5 adds an explicit runner opt-in without changing what `expose.paths`
means for standalone exposure:

```json
{
  "routeup": {
    "name": "myapp",
    "script": "dev:app",
    "expose": {
      "enabled": true,
      "paths": ["/api/webhooks/*"]
    }
  }
}
```

When enabled, runner mode obtains the public route before starting the child so
it can inject the granted URL into `ROUTEUP_URL`. Phase 8.5 implements this
config shape and owns the route, tunnel, and child lifecycle together.

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
Token hashing:       crypto/sha256 (tokens are high-entropy; no KDF needed)
```

Avoid `viper` at first. Config needs are still unsettled, so a small explicit config loader is better than a large config framework.

The local agent has no persistent storage. Route registry and the 1024-entry
request-log ring stay in memory. Disk-backed log storage is out of scope for v1.

The public server uses SQLite for token storage, claim tracking, and grace-window state. `modernc.org/sqlite` is chosen specifically so the server cross-compiles cleanly without a cgo toolchain. Add `sqlc` only when query count or scan complexity actually hurts.

## Code Structure

Start with this layout once implementation begins:

```txt
cmd/routeup/main.go

internal/cli/
  root.go
  expose.go
  inspect.go
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

internal/ipc/
  ipc.go        wire types + path constants shared by agent and CLI

internal/agent/
  agent.go           daemon lifecycle (server side of the IPC)
  api.go
  registry.go

internal/agentctl/
  client.go          CLI-side stub that talks to the agent

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
  capture.go
  store.go

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

The CLI↔agent code is split by role: `internal/agent` is the daemon (registry, API handlers, reverse proxy), `internal/agentctl` is the CLI-side stub that calls it, and `internal/ipc` holds the wire types both import. This keeps the daemon out of the CLI's dependency graph and avoids confusing the two `Register` methods (one mutates the registry, one sends a request).

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
The agent owns connections      tunnels and active proxy state
The foreground CLI owns claims  route registrations, exposure, child processes
```

Foreground commands normally release their own registrations and exposures on
exit. If one crashes, the agent reaps state owned by its dead PID and tears down
matching connections. Other active claims and connections are unaffected. No
`proxy start` or `proxy stop` style commands are exposed.

CLI-to-agent IPC:

```txt
Transport:   Unix domain socket per user
Path:        ~/.routeup/agent.sock (default), $XDG_RUNTIME_DIR/routeup/agent.sock on Linux when available
Permissions: 0700 directory, 0600 socket
Wire format: JSON over HTTP/1.1
Auth:        filesystem permissions only
Versioning:  /v1/ URL prefix; GET /v1/status returns agent version and boot id
```

## Public Server

The public server receives external traffic and forwards it to connected clients.

DNS:

```txt
routeup.dev   -> server IP
*.routeup.dev -> server IP
```

### Public hostname model

Every public host is **one label under a namespace base**: `<label>.<base>`.
This is what keeps hosts coverable by a single wildcard certificate
(`*.<base>`), since publicly-trusted wildcards match exactly one label. There
are three tiers, all expressed by a token's allow patterns:

```txt
tier        token            allow pattern         public host
try         none             — (public_namespace)  <label>.try.routeup.dev
root        required         *.routeup.dev         <label>.routeup.dev
namespace   required         *.mukul.routeup.dev   <label>.mukul.routeup.dev
```

The CLI turns a dotted local route into one public label by replacing dots with
hyphens. The server still rejects raw multi-label claims at its trust boundary.

Reserved names (`api`, `admin`, `www`, …, the control host) protect only the
**root** tier — inside an owned namespace the tenant may use any label, so
`api.mukul.routeup.dev` belongs to mukul. Granting a namespace also reserves
its label at the root tier, so a `*.routeup.dev` token cannot grab
`mukul.routeup.dev` out from under it. The token's allow `*` and the TLS
wildcard `*` therefore mean the same thing: exactly one label.

### Reserved subdomains

The server refuses to claim:

```txt
api, admin, app, dashboard, dev, docs, status, www, try
```

The list lives in server config so an operator can extend it. These names are reserved for the server itself (future operator UI, control endpoints), for common conventions users will assume are taken, and for the public namespace below. The chosen public-namespace subdomain is added to this list automatically.

### Public namespace

The server may designate one subdomain as a **public namespace** that anyone can claim under without a token:

```txt
routeup expose --random --port 8080
-> https://lively-otter-4f2.try.routeup.dev (random, session-only)

routeup expose foo --port 8080
-> https://foo.try.routeup.dev (first-come-first-served, session-only)
```

All public-namespace claims release on disconnect. There is no grace window, no
persistence, and no token. Within the namespace, names are
first-come-first-served and a held name returns `409`. `--random` chooses a
random label before the claim; it does not retry a collision.

The public namespace is **off by default** on self-hosted servers. Enable via server config:

```txt
public_namespace: try
```

Set to empty to disable. The hosted `routeup.dev` deployment enables `try`;
self-hosted operators opt in explicitly.

### Tokens

Tokens authorize persistent, scoped claims outside the public namespace.

Shape:

```txt
sk_routeup_<43-char base64url>
```

The `sk_` prefix is the Stripe-style "secret key" convention; SAST tools (gitleaks, trufflehog, GitHub secret scanning) recognize the pattern and flag accidental commits. The random part is 32 bytes from `crypto/rand`, base64url-encoded without padding. The server stores only a SHA-256 hash of the secret in SQLite; plaintext is shown once at creation and never again. The secret is 256-bit random, so a fast hash without salt or KDF is sufficient — a KDF would only add verification cost.

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

The public server always serves HTTPS — there is no plaintext mode. By default it obtains and renews wildcard certificates automatically via Let's Encrypt using the **ACME DNS-01** challenge, driven by **`caddyserver/certmagic`** with the **Cloudflare** DNS provider (`libdns/cloudflare`). The operator supplies one scoped Cloudflare API token (`CLOUDFLARE_API_TOKEN`, Zone.DNS:Edit on the zone); certmagic handles issuance, renewal, storage, and locking.

Following the one-label-per-namespace model, the server manages a wildcard per namespace base: `*.<domain>` (the root tier and the control host) and `*.<public-namespace>.<domain>` are obtained at startup; `*.<namespace>.<domain>` is obtained lazily on the first claim into a token namespace. Per-namespace wildcards (rather than per-host certs) keep the certificate count bounded and Let's Encrypt rate limits comfortable.

The other mode is `cert`: an operator-provided certificate/key (e.g. a Cloudflare origin cert, or a self-signed cert for local development). DNS provider and ACME library choices (formerly OQ-014/OQ-015) are decided as above.

## Logs (Phase 9) And Request Capture/Inspect (Phase 10)

Route logs are first-class.

Where logs live:

```txt
agent:  canonical record for every request handled by a local route or tunnel
        1024-entry in-memory request ring
server: no request-log store in the current implementation
```

The agent holds the authoritative log for requests that reach a registered route
or active tunnel. `routeup logs` reads from the agent. If no live tunnel exists,
the public server returns `503` before the request reaches the agent, so there is
no agent-side entry. Server-side request records, counters, and offline-request
logging remain open under OQ-012 rather than being implied by Phase 9.

Available commands:

```bash
routeup logs
routeup logs myapp
routeup logs api.myapp
routeup logs api.myapp --follow
routeup logs api.myapp --follow --plain
routeup logs api.myapp --public
routeup logs api.myapp --local
routeup logs api.myapp --json
routeup logs api.myapp --since 10m --method POST --status 202 --limit 50
```

Default log line:

```txt
12:41:03 public api.myapp POST /api/webhooks/github 200 38ms req_Ap7kQ3mN8vR2xLzC
12:41:07 local  myapp     GET  /settings             200 12ms req_B9vL5rTs1mX8qK2d
```

In an interactive terminal, `--follow` opens a Bubble Tea live viewer with a
bounded 200-row display, scrolling, reconnect handling, and responsive columns.
`--plain` forces append-only lines in a terminal, redirected output selects that
format automatically, and `--json` writes NDJSON. Human terminal output uses
Lip Gloss styling and honors `NO_COLOR`; machine output never contains ANSI
formatting.

Request IDs are compact opaque values in `req_<16-char-random>` form. The log
line already carries the route, method, and path, so IDs stay short enough to
copy into the inspection commands.

Phase 10 adds opt-in request retention and inspection:

```json
{
  "capture": {
    "request": true,
    "response": true,
    "redact_headers": ["authorization", "cookie"]
  }
}
```

Capture is disabled by default. Request and response retention are enabled
independently in `routeup.json` or the `package.json` `routeup` block after path
and target matching. Each captured direction is limited to 256 KiB including
headers and body, and all data remains in the agent's in-memory request ring.
`redact_headers` is case-insensitive: those headers are forwarded normally but
omitted from retained data and listed by `routeup inspect`. Oversized or
partially-read messages retain a bounded prefix and report `Complete: false`;
blocked public paths and unmatched targets are logged without capture.

```bash
routeup inspect req_Ap7kQ3mN8vR2xLzC
```

`routeup inspect` safely escapes retained values by default. `--raw` emits
unescaped retained values, and `--json` emits the complete exchange with byte
bodies encoded as base64.

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

- Homebrew tap for macOS and Linux: `brew install mukul-mehta/tap/routeup`.
- GitHub releases for direct download (tarball + checksums).

Lifecycle commands:

```bash
routeup update     # check for and install a newer release
routeup uninstall  # remove agent, CA, certs, and state dir
```

`routeup update` detects the install channel (Homebrew vs direct binary) and
delegates to the appropriate updater. `routeup uninstall` must work even when
the binary is being replaced: it stops the agent, removes the macOS port
forwarder or Linux capability, removes the local CA from the trust store,
deletes generated certificates, and removes `~/.routeup/`.

## Non-Goals For V1

Do not build these in v1:

```txt
OAuth access protection
team accounts
billing
hosted SaaS control plane
web UI
worktree routing
Windows support
complex ACLs
```

Possible later additions:

```txt
basic auth for public routes
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
