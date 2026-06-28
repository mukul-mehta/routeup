# Walkthrough — How to Read the routeup Source

How the full routeup codebase is organized and how to trace a request through
every layer: route model, local agent, CLI, public server, and tunnel.

---

## Source tree

```
cmd/routeup/
  main.go                  # Entry point: calls cli.Execute()

internal/
  route/                   # Route names, parsing, host mapping, random names
    name.go                #   Name struct, Parse, LocalHost, PublicHost
    constants.go           #   LocalSuffix ("localhost"), PublicSuffix ("routeup.dev")
    random.go              #   RandomName() via golang-petname

  config/                  # Config file discovery and value resolution
    discovery.go           #   Discover: routeup.json or package.json lookup
    packagejson.go         #   read package.json "routeup" block
    resolve.go             #   Resolve: flag > env > file config precedence
    config.go              #   Config struct, LoadRouteupJSON, Validate

  ipc/                     # Shared wire types (imported by agent + agentctl)
    ipc.go                 #   Claim, Status, ExposeRequest, ExposeResponse,
                           #   UnexposeRequest, ConflictError, path constants

  certs/                   # Local CA and per-SNI TLS issuer
    ca.go                  #   CA struct, Create, Load, EnsureCA, Inspect
    leaf.go                #   Issuer: per-SNI leaf certificates
    trust_darwin.go        #   OS trust (macOS)
    trust_linux.go         #   OS trust (Linux)
    local.go               #   EnsureLocalCA() convenience

  state/                   # Filesystem paths and state helpers
    constants.go           #   File/dir/env names
    paths.go               #   AgentSocketPath, CACertPath, etc.
    setupmarker.go         #   Setup marker file
    clientconfig.go        #   ClientConfig (saved server URL + token)

  proxy/                   # Local HTTPS reverse proxy
    local.go               #   PortLookup interface, New handler

  agentctl/                # CLI-side agent IPC client stub
    client.go              #   Client: Status, Register, Unregister, List
    lifecycle.go           #   EnsureRunning, Stop, Restart, spawnAndWait
    expose.go              #   Expose, Unexpose
    reconcile.go           #   MaintainClaim loop (re-register on agent restart)

  agent/                   # Local agent daemon
    agent.go               #   Agent struct, Run (UDS API + TLS proxy), reap
    registry.go            #   Registry: in-memory route claims, conflict detection
    api.go                 #   HTTP handlers: register, unregister, list, status,
                           #   shutdown, expose, unexpose
    expose.go              #   tunnelManager, tunnelSession, newTunnelProxy

  tunnel/                  # Tunnel protocol (shared by agent + server)
    protocol.go            #   HandshakeMessage, ClaimSpec, RouteBroker interface
    client.go              #   agent side: handshake + http.Server.Serve(session)
    server.go              #   TunnelRegistry: accept, per-session ReverseProxy
    timeouts.go            #   dial / backoff / request-header timeouts

  streamtest/              # Synthetic WS/SSE/large-body backends for M6 tests

  server/                  # Public routeup server
    doc.go                 #   Package doc
    config.go              #   ServerConfig, validation
    wire.go                #   Path constants
    server.go              #   Server struct, Run, attach
    background.go          #   runReap, runCertPrewarm (background loops)
    api.go                 #   HTTP mux, serveIngress, health
    authorize.go           #   Authorizer: ClaimAttempt -> Decision
    allow.go               #   AllowPattern: "*.<suffix>" parse/matches
    host.go                #   DeriveTokenHost, DeriveNamespaceHost, validators
    tokens.go              #   CreateToken, VerifyToken, RevokeToken, ListTokens
    holds.go               #   RouteHold persistence, conflict, grace window
    broker.go              #   routeBroker: tunnel.RouteBroker implementation
    store.go               #   SQLite open/close
    migrations.go          #   Schema DDL
    reserved.go            #   DefaultReservedLabels, ReservedSet
    tls.go                 #   certManager: ACME or static TLS

  cli/                     # Cobra command tree
    root.go                #   Root command, subcommand registry
    serve.go               #   `routeup serve` — local + optional expose
    expose.go              #   `routeup expose` — public tunnel only
    server.go              #   `routeup server` — run the public server
    servercfg.go           #   Server config file paths
    token.go               #   `routeup token create|list|revoke`
    agent.go               #   `routeup agent run|stop|restart`
    setup.go               #   `routeup setup` — local CA + OS trust + port bind
    forward.go              #   `routeup forward` — internal macOS 443 forwarder (hidden)
    doctor.go              #   `routeup doctor` — diagnostics
    routes.go              #   `routeup routes` — list active local routes (+ public)
    logs.go                #   `routeup logs` — access logs
    update.go              #   `routeup update` — self update
    uninstall.go           #   `routeup uninstall` — reverse setup

  update/                  # Self-update mechanism
  privbind/                # Privileged port binding helpers (platform-specific)
```

---

## Route model (`internal/route/`)

A route name is a dotted, DNS-label-style identifier validated by `route.Parse`
(`name.go:29`). Rules: 1+ labels of `[a-z0-9-]`, each 1-63 chars, no leading/
trailing hyphens, max 253 total. Input is lowercased. Rejects suffixes
`.localhost` and `.routeup.dev` (reserved).

`Name.String()` returns the dotted form. `LocalHost()` appends `.localhost`;
`PublicHost()` appends `.routeup.dev`.

Constants (`constants.go`):
- `LocalSuffix = "localhost"` — RFC 6761 reserved, no DNS needed
- `PublicSuffix = "routeup.dev"` — the public server domain

`RandomName()` (`random.go:7`) generates a docker-style two-word name via
`golang-petname`, used by `--random` on `serve`/`expose`.

---

## Config discovery (`internal/config/`)

`config.Discover(cwd)` scans the working directory for `routeup.json` or a
`package.json` with a `"routeup"` block (`discovery.go`). Returns the first
match or an empty `Config`.

`config.Resolve(inputs)` resolves a route name and port by precedence:
flag > env var > file config. Bare names are prefixed with the project name;
dotted names are taken literally (`resolve.go`).

---

## CLI command tree (`internal/cli/`)

Entry point: `cmd/routeup/main.go` → `cli.Execute()` → `cobra.Command`.

Root command (`root.go`): `routeup`, with subcommands registered in
`newRootCmd()`:

```
routeup
  # user-facing
  setup                     One-time machine setup: local CA, OS trust, port 443
  serve [name]              Serve a local app on https://<name>.localhost (optionally --expose)
  expose [name]             Expose a local port publicly through a routeup server
  routes                    List active local routes
  doctor                    Diagnose setup state
  update                    Self-update to the latest release
  uninstall                 Reverse setup (untrust CA, remove forwarder/setcap, delete state)
  logs                      Stream request logs  [stub: "not implemented yet"]
  agent                     Inspect/control the local agent: status | start | stop | restart

  # hidden — operator commands, run on the server host
  server                    Run the public server
  token create|list|revoke  Manage public-server tokens (opens the server SQLite DB directly)

  # hidden — internal
  agent run                 The daemon entrypoint (spawned on demand, not run by hand)
  forward <from> <to>       TCP byte-pipe used by the macOS port-443 LaunchDaemon
```

`server`, `token`, `agent run`, and `forward` are `Hidden: true` in cobra — real
commands, just kept out of `--help`. `server` and `token` are operator commands
that open the server's database directly (the server need not be running).

The `serve` command (`serve.go`): resolves the route name and port, ensures the
local CA exists, ensures the agent is running, registers the route claim with
the agent, optionally calls `serveExpose` (same path as `expose`), then blocks
on `MaintainClaim` — a 2s reconcile loop that re-registers the claim on agent
restart.

The `expose` command (`expose.go`): resolves server URL + token (flag > env >
saved client config), resolves the route name, calls `startTunnel` which starts
the agent, calls `holdExposure` → agent IPC over the Unix socket, prints the
public URL, and blocks on Ctrl-C.

---

## IPC types (`internal/ipc/`)

Shared between the agent daemon and the CLI client stub. No behavior, just
types:

```go
type Claim struct {
    Name         string    // dotted route name
    Port         int       // local app port
    OwnerPID     int       // CLI's PID (for reap)
    OwnerCWD     string    // (for conflict messages)
    RegisteredAt time.Time
}

type Status struct {
    Version, BootID, ExecPath string
    UptimeSeconds             int64
    TLSAddr                   string
    ExecModTime               time.Time
}

type ExposeRequest struct {
    Name, Server, Token string
    Port, OwnerPID      int
}

type ExposeResponse struct {
    Host string
}

type UnexposeRequest struct {
    Host string
}
```

Path constants: `/v1/status`, `/v1/routes`, `/v1/shutdown`, `/v1/expose`,
`/v1/unexpose`.

---

## Agent daemon (`internal/agent/`)

### Agent struct and Run (`agent.go`)

Created once per machine by `agentctl` when the CLI needs an agent. Key fields:

```go
type Agent struct {
    reg      *Registry       // in-memory route claims
    tunnels  *tunnelManager  // M5: public tunnel sessions
    sockPath string          // Unix socket for CLI IPC
    tlsAddr  string          // internal high-port TLS listener (127.0.0.1:47443); 443 reaches it post-setup
    bootID   string          // random, changes on every start (reconcile signal)
}
```

`Run(ctx)` (`agent.go:93`):

1. Loads or creates the local CA.
2. Binds a Unix socket (chmod 0600) for the control API.
3. Binds a TLS listener with a per-SNI issuer (mints a leaf per SNI under
   `.localhost` on demand, cached).
4. Creates the `tunnelManager` (M5: for public exposure).
5. Starts two HTTP servers in parallel:
   - **API server** — serves the control mux on the Unix socket
   - **Proxy server** — reverse proxy on the TLS listener
6. Starts a 10s reap loop that drops stale claims and orphaned tunnels.
7. Blocks on ctx cancel, shutdown API, or a listener error.

### Registry (`registry.go`)

In-memory `map[string]ipc.Claim`. Key methods:

- `Register(c)`: inserts or replaces. If owned by a live different PID,
  returns `ipc.ConflictError`. Same PID can update in place.
- `Unregister(name)`: idempotent delete.
- `List()`: sorted snapshot.
- `LookupPort(name)`: used by the proxy to find the upstream port.
- `Reap()`: drops claims whose PID is dead (signal-0 probe).

The registry is purely in-memory because the CLI's `MaintainClaim` loop
re-registers after any agent restart.

### Proxy wiring

The agent wires `proxy.New(a.reg, a.logger)` onto its TLS listener. For each
incoming HTTPS request, `proxy.New` extracts the route name from the Host
header (strip port, strip `.localhost`), looks up the port via `a.reg`, and
reverse-proxies to `localhost:<port>`. Unknown hosts get a `text/plain` 404.

### Tunnel manager (M5: `expose.go`)

See the M5 section below for `tunnelManager`, `tunnelSession`, and
`newTunnelProxy`.

### API handlers (`api.go`)

Seven endpoints over the Unix socket:

- `POST /v1/routes` — register a claim
- `DELETE /v1/routes/{name}` — unregister
- `GET /v1/routes` — list claims
- `GET /v1/status` — agent status + boot ID
- `POST /v1/shutdown` — graceful shutdown
- `POST /v1/expose` — start a tunnel (M5)
- `POST /v1/unexpose` — stop a tunnel (M5)

---

## CLI agent client (`internal/agentctl/`)

The CLI stub that speaks to the agent over the Unix socket.

### Client (`client.go`)

Wraps `http.Client` with a Unix-socket `DialContext`. Methods: `Status`,
`Register`, `Unregister`, `List`.

### Lifecycle (`lifecycle.go`)

`EnsureRunning(ctx)`: the core agent lifecycle function.

1. If an agent is reachable and not stale → `EnsureAlreadyRunning`.
2. If stale → stop it, spawn a fresh one → `EnsureRestarted`.
3. If unreachable → spawn one → `EnsureStarted`.

Spawn: `exec.Command("routeup", "agent", "run")`, setsid-detached, stdout/stderr
redirected to the agent log file. Waits up to 5s for `/v1/status` to respond.

`Stop(ctx)`: graceful shutdown via `POST /v1/shutdown`, fallback to SIGTERM via
PID file.

`IsStale(status)`: compares Version and binary mod time to detect a rebuilt
CLI.

### MaintainClaim (`reconcile.go`)

A 2s ticker loop that re-registers the route claim if the agent's BootID
changes or the agent becomes unreachable. Runs for the lifetime of `serve`.

### Expose (`expose.go`)

`Expose(ctx, req)`: sends `POST /v1/expose`, bounded by the caller's ctx (with a
generous fallback when the ctx carries no deadline; budgets live in
`timeouts.go`). `Unexpose(ctx, host)`: sends `POST /v1/unexpose`, idempotent.
The shared `http.Client` no longer sets a blanket timeout — each call's ctx
bounds it.

---

## Proxy (`internal/proxy/`)

`PortLookup` interface (`local.go:35`): breaks the import cycle between
`agent` (which imports proxy) and `proxy` (which needs the registry).

`New(lookup, logger)`: extracts the route name from `Host` by stripping
`.localhost` (and any port), looks up the port, and creates a one-shot
`httputil.ReverseProxy` per request. The `Rewrite` func preserves the
incoming Host header and sets `X-Forwarded-*`.

Unknown hosts return a `text/plain` 404 with a hint to run `routeup routes`.

---

## Local CA (`internal/certs/`)

`CA` struct (`ca.go:29`): `Cert *x509.Certificate`, `Key *ecdsa.PrivateKey`,
`CertPEM []byte`.

`Create(certPath, keyPath)`: generates an ECDSA P-256 self-signed CA, 10-year
validity, writes PEM files (cert 0644, key 0600).

`EnsureCA(certPath, keyPath)`: returns the `CA` if present and not expired,
or an error suggesting `routeup setup`.

`EnsureLocalCA()` (`local.go:13`): convenience wrapper resolving default paths,
used by CLI commands that need the agent.

`Issuer` (`leaf.go`): mints one TLS leaf per SNI on demand and caches it. The
SNI must end in the allowed suffix (`.localhost`); each distinct host
(`api.myapp.localhost`, `web.myapp.localhost`) gets its own 825-day leaf signed
by the CA. Not a wildcard — a fresh cached leaf per name.

Platform trust: `trust_darwin.go` uses `security add-trusted-cert`,
`trust_linux.go` copies to `/usr/local/share/ca-certificates/` and runs
`update-ca-certificates`.

---

## State helpers (`internal/state/`)

Resolves filesystem paths under `~/.routeup/`:

| Function | File | Purpose |
|---|---|---|
| `Dir()` | `~/.routeup/` | State root |
| `AgentSocketPath()` | `agent.sock` | CLI↔agent IPC |
| `AgentLogPath()` | `agent.log` | Spawned agent stdout/stderr |
| `AgentPIDPath()` | `agent.pid` | Running agent PID |
| `CACertPath()` | `ca.crt` | Local CA certificate |
| `CAKeyPath()` | `ca.key` | Local CA private key |
| `ReadClientConfig()` | `client.json` | Saved server URL + token |

On Linux, `AgentSocketPath` prefers `$XDG_RUNTIME_DIR/routeup/agent.sock`.
All paths can be overridden by `ROUTEUP_AGENT_SOCKET`.

---

## Local request flow

```
Browser
  -> https://api.myapp.localhost          (port 443 after setup)
  -> [macOS] root LaunchDaemon forwards 443 -> 127.0.0.1:47443
     [Linux] agent binds 443 directly (cap_net_bind_service); no forwarder
  -> agent's TLS listener (per-SNI leaf via Issuer, cached by SNI)
  -> proxy.New handler
  -> stripPort → strip ".localhost" suffix → "api.myapp"
  -> reg.LookupPort("api.myapp") → port 3000
  -> httputil.ReverseProxy -> http://localhost:3000 (preserves Host, sets X-Forwarded-*)
  <- response
```

No server contact, no token — this is the zero-network path that `routeup setup`
alone enables. The agent's TLS listener always binds an internal high port
(`127.0.0.1:47443` by default); how `:443` reaches it differs by OS (see
Privileged port binding below). Before setup, or with `setup --port 8443`, the
URL carries that port instead.

---

## Privileged port binding (`internal/privbind/`)

Binding `:443` needs privilege, but routeup runs the agent as your user with no
per-run sudo. `privbind` installs the per-OS machinery once at `setup` (the only
sudo prompt) so runtime needs none. `Required(userPort)` is true for ports
below 1024.

- **macOS**: `setup` installs a root LaunchDaemon (`dev.routeup.forwarder`,
  `RunAtLoad` + `KeepAlive`) that runs `routeup forward 127.0.0.1:443
  127.0.0.1:47443` — the hidden TCP byte-pipe in `cli/forward.go` (which refuses
  non-loopback targets). The agent stays on its high port; `AgentBindPort`
  returns 47443.
- **Linux**: `setup` runs `setcap cap_net_bind_service=+ep` on the binary, and
  `AgentBindPort` returns 443 so the agent binds it directly — no forwarder.

The capability is attached to the binary's inode, so a package upgrade drops it;
`routeup doctor` detects this (via `getcap`) and `ReapplyBind` (re-run `setup`)
restores it. macOS is unaffected because the LaunchDaemon references the stable
binary path. `setup` records the chosen port and binary path in the setup marker
(`~/.routeup/setup.json`); `TLSPortOrDefault` reads it back (default 443).

---

## M5: Public server, tunnel, and CLI expose

Phase 5 adds three interconnected systems:

1. **Token management** — create, verify (SHA-256), list, revoke. Tokens carry
   allow patterns that restrict which public hosts they can claim.
2. **Tunnel protocol** — WebSocket + yamux multiplexing between the agent and
   the public server.
3. **Public server** — HTTPS ingress, tunnel session management, authorization,
   route hold persistence (SQLite), and lazy wildcard certificates via ACME.

### Token system

**Token struct** (`tokens.go:20`):

```go
type Token struct {
    ID        string          // 6 random bytes, hex-encoded (12 hex chars)
    Name      string          // human label
    Patterns  []AllowPattern  // e.g. ["*.routeup.dev", "*.mukul.routeup.dev"]
    CreatedAt time.Time
    RevokedAt *time.Time
}
```

**AllowPattern** (`allow.go:15`): parse from `"*.<suffix>"`. The `*` matches
exactly one label (DNS-safe, cert-coverable). `Matches(host)` strips the suffix
and checks the remainder is non-empty and dot-free.

**CreateToken** (`tokens.go:34`):
1. `secret` = `sk_routeup_` + 32 bytes `crypto/rand`, unpadded base64url.
2. `id` = 6 random bytes, hex-encoded.
3. INSERT SHA-256 hash into `tokens` with allow patterns, in a transaction.
4. Return `(id, secret)`. Plaintext shown once; never stored.

**VerifyToken** (`tokens.go:78`):
1. Reject if not `sk_routeup_`-prefixed.
2. SHA-256 lookup: `SELECT ... WHERE token_hash = ? AND revoked_at IS NULL`.
3. Load patterns, return `*Token` or `ErrTokenInvalid`.

### Tunnel protocol

**Stack**: TLS → WebSocket (one byte pipe; the agent dials *out*, NAT-friendly)
→ yamux (many independent streams over that pipe) → HTTP per stream. Agent =
yamux client, server = yamux server. **Stream 0** is the control channel; each
later stream is one public request. One route per session.

**Wire types** (`protocol.go`): `HandshakeMessage{Type, Claim *ClaimSpec, Granted
string, Error, Code}` with types `claim` / `claim_ok` / `claim_err`;
`ClaimSpec{Route}`. The `RouteBroker` interface (`Hold` + `Release`) is the seam
that keeps the tunnel package ignorant of tokens, SQLite, and certs. Timeouts
live in `timeouts.go`.

**The HTTP on each stream is stdlib, not hand-rolled** — the trick is that the
HTTP roles are inverted from the yamux roles (agent = HTTP server, public server
= HTTP client), and yamux objects satisfy the stdlib interfaces:

**Client / agent side** (`client.go`):
- `Run(ctx)`: reconnect loop with backoff; returns on `PermanentError` (claim
  rejected) or ctx cancel.
- `handshake(ctx)`: dial `wss://…/_routeup/tunnel` (version header + Bearer),
  `yamux.Client`, open stream 0, send the claim, await `claim_ok`, fire
  `onGranted(host)`.
- `serve(ctx, session, ctrl)`: hands the session straight to a standard
  `http.Server` via **`srv.Serve(session)`** — `*yamux.Session` satisfies
  `net.Listener`. The server reads each request off its stream, runs it through
  the agent's local reverse proxy (`newTunnelProxy` → `localhost:<port>`), and
  serializes the response back. A ctx-cancel watcher closes the session so
  `Serve` returns.

**TunnelRegistry / server side** (`server.go`):
- `AcceptHandler()`: checks version, extracts the Bearer token, upgrades to
  WebSocket, runs `ServeConn`.
- `ServeConn`: `yamux.Server` → accept stream 0 → `keeper.Hold(spec)` →
  `register(host, session)` → `claim_ok` → block until the control stream EOFs
  (agent disconnect), then `release(host)`.
- `register` stores a per-session `httputil.ReverseProxy` (`newSessionProxy`)
  whose `http.Transport.DialContext` returns `session.Open()` — each public
  request opens a fresh yamux stream. `Handler(host) (http.Handler, bool)`
  returns that proxy for ingress to drive. (No more `Forward`/`streamBody`.)

### Public server (`internal/server/`)

**Server struct**: `cfg`, `store` (SQLite), `authorizer` (Authorizer),
`tunnels` (TunnelRegistry), `cm` (certManager). The background loops
(`runReap`, `runCertPrewarm`) live in `background.go`; `server.go` keeps `Run`
and wiring.

**Server.Run** (`server.go:77`):
1. Open store, run migrations, purge ephemeral holds.
2. Wire components: authorizer, routeBroker, TunnelRegistry.
3. Reap loop (10s) for expired grace holds.
4. Build cert manager (ACME with certmagic + Cloudflare DNS-01, or static).
5. Cert prewarm loop (60s): manage `*.<base>` wildcards for each token namespace.
6. Serve HTTPS on `cfg.Listen` (`ListenAndServeTLS`; certs come from the
   `TLSConfig`, so the file arguments are empty).
7. On ctx cancel or a fatal listener error, graceful shutdown in order:
   HTTP server (`Shutdown`, 5s timeout) → cancel + await reap loop → await
   prewarm loop.

**handler() routing** (`api.go`): paths under `ControlPrefix` (`/_routeup`) go to
the control mux — `GET /_routeup/v1/health` and the tunnel endpoint
`/_routeup/tunnel` (`AcceptHandler`). Everything else is ingress: `serveIngress`
strips the port from `Host`, looks up the per-session handler via
`tunnels.Handler(host)`, and calls `h.ServeHTTP(w, r)` — the `ReverseProxy` does
header hygiene, streaming, and flushing. Map miss → 503; a round-trip failure →
502 (the proxy's `ErrorHandler`). A path prefix (not Host matching) marks control
traffic so the server's own control host can be an ordinary subdomain.

**Authorizer** (`authorize.go`): resolves `ClaimAttempt{TokenSecret, Route}` to
`Decision{Host, Mode, Base, Ephemeral, holdReq}`.
- Token path: `VerifyToken` → `resolvePublicLabel` → `placeUnderToken`
  (iterate allow patterns, check reserved + namespace label conflicts).
- Public namespace path: when `TokenSecret == ""` and `PublicNamespace` is set,
  creates ephemeral `<label>.<ns>.<domain>` hold.

**routeBroker** (`broker.go`): the `tunnel.RouteBroker` implementation.
`Hold`: authorize → `Store.HoldRoute` (conflict check + upsert) →
`ensureNamespaceCert` (lazy ACME). `Release`: set grace window for token holds,
immediate delete for namespace holds.

**Store** (`store.go`, `holds.go`, `tokens.go`, `migrations.go`): SQLite backing
via `modernc.org/sqlite` (pure-Go, no cgo), opened WAL with a 5s busy timeout and
`foreign_keys` on; `OpenStore` runs the migrations. Tables: `tokens`,
`token_allow_patterns`, `route_holds`. `HoldRoute` serializes through a
process-level `holdMu` so the read-check-upsert conflict decision is atomic.
Grace window: 30s for token holds (`holds.go:11`), same token resumes within the
window; namespace holds are ephemeral (deleted on release and purged at startup).

### Agent-side expose

**tunnelManager** (`agent/expose.go`): created in `Agent.Run`, keyed by public
host. `Expose(reqCtx, req)` splits the IPC context from the tunnel context
(derived from the agent's lifetime), creates `tunnel.NewClient`, and awaits the
grant or an error — the tunnel keeps serving after the IPC reply returns.
`Unexpose(host)` cancels the tunnel context; `ReapDeadOwners` (10s, signal-0
probe) tears down tunnels whose owning CLI died; `publicHosts()` maps owner PID →
host so `routeup routes` can show a `PUBLIC` column.

**newTunnelProxy**: a single-target `httputil.ReverseProxy` to `localhost:<port>`
that preserves the public Host header. It is the `Handler` the agent's
`http.Server` runs for each request stream.

---

## HTTP over yamux (the stdlib refactor)

Neither end hand-rolls HTTP framing. The insight is that **the HTTP roles are
inverted from the yamux roles**, and yamux objects satisfy the stdlib interfaces:

| | Transport (yamux) | Application (HTTP) |
|---|---|---|
| Agent | dials, yamux **client** | HTTP **server** — `http.Server.Serve(session)` |
| Public server | accepts, yamux **server** | HTTP **client** — `http.Transport` over `session.Open()` |

- **Agent**: `*yamux.Session` *is* a `net.Listener` (its `Accept` yields one
  stream per inbound request), so `serve` is just `http.Server.Serve(session)`.
  The stdlib parses each request, runs `newTunnelProxy`, and serializes the
  response — framing, flushing, and cancellation all handled for you.
- **Public server**: `newSessionProxy` is an `httputil.ReverseProxy` whose
  `http.Transport.DialContext` returns `session.Open()` — "to reach this agent,
  open a yamux stream." Ingress just calls `Handler(host).ServeHTTP(w, r)`.

yamux is unchanged and more central than ever — it's literally the `net.Listener`
the agent serves and the `net.Conn` source the server dials. This deleted the
hand-rolled `streamResponseWriter`, `serveStream`, `Forward`, `streamBody`, and
the manual header copy (~150 lines), and — because `ReverseProxy` flushes —
SSE/streaming and WebSocket upgrades work without bespoke framing. M6 keeps that
shape and tests it with synthetic WebSocket HMR, SSE HMR, large-body, and
cancellation scenarios.

---

## Tests as entry points

The fastest way to understand a subsystem is the test that exercises it
end-to-end:

| Test | File | What it shows |
|---|---|---|
| `TestIngress_EndToEnd` | `server/ingress_test.go:20` | The whole public path: real store + authorizer + registry, a live `tunnel.Client`, and a public request routed by `Host` reaching the agent's backend. The single best place to start. |
| `TestIngress_NoTunnel503` | `server/ingress_test.go:88` | Ingress returns 503 when no tunnel holds the host. |
| `TestTunnel_WebSocketHMR` | `tunnel/tunnel_test.go` | Synthetic Vite-style WebSocket HMR through yamux: upgrade, subprotocol, server push, and client echo. |
| `TestTunnel_SSEStreamsIncrementally` | `tunnel/tunnel_test.go` | Synthetic Next-style SSE through yamux; proves events flush before stream close. |
| `TestTunnel_LargeBodyEcho` | `tunnel/tunnel_test.go` | Large request and response body streaming through one tunnel stream, hash-checked end-to-end. |
| `TestIngress_ClientDisconnectCancelsUpstream` | `server/ingress_test.go` | Public client disconnect propagates cancellation back to the agent-side backend. |
| `TestLocalProxy_WebSocketHMR` / `TestLocalProxy_SSEStreamsIncrementally` | `proxy/local_test.go` | The local `.localhost` proxy path handles the same HMR-style WS/SSE traffic. |
| `TestIntegration_ViteHMR` / `TestIntegration_NextHMR` | `server/integration_test.go` | Real Vite and Next dev servers exposed through `serveIngress`; drives the actual HMR WebSocket and asserts a file edit produces a live HMR push. Build-tagged (`integration`), excluded from the default suite — run with `just test-integration`. |
| `TestAuthorize_*` | `server/authorize_test.go` | Placement rules: root vs namespace tier, reserved labels, out-of-domain and multi-label rejection. |
| `TestHold_*` | `server/holds_test.go` | The hold/grace state machine: active conflict, token grace resume, grace expiry, ephemeral namespace holds. |
| `TestCreateAndVerifyToken` | `server/tokens_test.go:30` | Token mint → SHA-256 store → verify round trip. |
| `TestServer_ServesHTTPS` | `server/tls_test.go:64` | The server actually terminates TLS. |

Suggested reading order — **follow a live public request**: `server/api.go`
(`serveIngress`) → `tunnel/server.go` (`Handler` + `newSessionProxy`) →
`tunnel/client.go` (`serve`: `http.Server.Serve(session)`) → `agent/expose.go`
(`newTunnelProxy` → `localhost:<port>`). Then **how the tunnel was established**:
`tunnel/protocol.go` (wire types + lifecycle diagram) → `tunnel/client.go`
(`handshake`) → `tunnel/server.go` (`ServeConn`) → `server/broker.go` (the
`RouteBroker` bridge) → `server/authorize.go` (policy) → `server/holds.go`
(persistence). Local `.localhost` path: `proxy/local.go` → `agent/agent.go` →
`certs/leaf.go`.
