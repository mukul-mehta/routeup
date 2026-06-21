# routeup

`routeup` gives local services stable HTTPS route names and can expose those same routes publicly when needed.

It is an open source developer tool for local apps, APIs, webhooks, OAuth callbacks, mobile testing, and browser/agent testing.

## Status

Phase 4 — Real local setup. `routeup setup` creates a local CA, trusts it in the OS keychain, and binds port 443 (a root LaunchDaemon forwarder on macOS, `setcap` on Linux). `routeup serve <name> --port <p>` then serves the app on `https://<name>.localhost` with a CA-signed cert. No public exposure yet.

## Implementation Progress

Currently: Phases 4 and 4.5 complete — per-machine local CA with on-demand per-SNI leaf certs, OS trust install, HTTPS on port 443, plus `setup`, `serve`, `routes`, `doctor`, `uninstall`, `update`, and `agent` commands. The agent terminates TLS on an internal high port; on macOS a tiny root forwarder bridges 443, on Linux the binary gets `cap_net_bind_service`. Ships as a single binary via Homebrew (`brew install mukul-mehta/tap/routeup`) and a curl installer, built and released by GoReleaser on tag.

Phase definitions and acceptance criteria live in [docs/MILESTONES.md](docs/MILESTONES.md).

- [x] **Phase 0 — Documentation:** PLAN, README, ARCHITECTURE, ENGINEERING-STANDARDS, MILESTONES, OPEN-QUESTIONS, AGENTS, LICENSE
- [x] **Phase 1 — Scaffolding & walking skeleton:** Go module, lint, CI, cobra root with `doctor`/`routes`/`logs` stubs
- [x] **Phase 2 — Route names & config discovery:** parser, hostname mapping, dry-run expose
- [x] **Phase 3 — Local agent on a high port:** registry, CLI↔agent IPC, reverse proxy by Host
- [x] **Phase 4 — Real local setup:** local CA, certificate generation, HTTPS on 443
- [x] **Phase 4.5 — Packaging & lifecycle:** `routeup uninstall`/`update`, upgrade-safe forwarder path, doctor bind check, Homebrew + curl install, GoReleaser pipeline
- [ ] **Phase 5 — Public server & tokens:** route claim API, token allow patterns, public namespace
- [ ] **Phase 6 — Tunnel MVP:** WebSocket + yamux, one public request reaches a local port
- [ ] **Phase 7 — Streaming, WebSockets, SSE:** real dev servers work through the tunnel
- [ ] **Phase 8 — Path proxy:** frontend + API behind one route
- [ ] **Phase 9 — Process runner:** child process with `PORT`/`HOST`/`ROUTEUP_*` env injection
- [ ] **Phase 10 — Route logs:** local/public, `routeup logs --follow`
- [ ] **Phase 11 — Inspect & replay:** opt-in header/body capture, `routeup inspect`/`replay`

## Quick Look

Run a local app on a stable HTTPS route:

```bash
routeup
```

For a service named `myapp`:

```txt
https://myapp.localhost
```

No signup, no token — just `routeup setup` once. The local agent serves `*.localhost` from there on.

Expose it publicly:

```bash
routeup expose --port 8080
```

What you get depends on whether you have a server token:

```txt
with a token:     https://myapp.<your-namespace>.routeup.dev   # persistent
without a token:  https://<random>.try.routeup.dev               # ephemeral, when the server enables its public namespace
```

Tokens are minted by the server operator. The local-only flow needs neither a server nor a token.

## Install

macOS and Linux (arm64/amd64).

Homebrew:

```bash
brew install mukul-mehta/tap/routeup
```

Or curl:

```bash
curl -fsSL https://get.routeup.dev | sh
```

Then run `routeup setup` once. Later, `routeup update` upgrades in place (or via `brew upgrade` for Homebrew installs).

> macOS: the binary is unsigned. `brew` and the `curl` installer run it fine; only a manual download from the Releases page is quarantined, cleared with `xattr -d com.apple.quarantine ./routeup`.

## Local HTTPS, today

Phase 4 is implemented: trusted HTTPS on `*.localhost`. Public exposure is not built yet.

One-time setup creates a local certificate authority, adds it to your OS trust store, and binds port 443:

```bash
routeup setup
```

You'll be asked for Touch ID or your password once. Then serve any local app on a trusted route:

```bash
routeup serve myapp --port 3000      # https://myapp.localhost
routeup serve api.myapp --port 8080  # https://api.myapp.localhost
```

Other commands:

```bash
routeup doctor      # check CA, OS trust, port 443, and agent health
routeup routes      # list what's currently served
routeup uninstall   # remove the cert, the port 443 helper, and ~/.routeup
```

### Non-browser clients

Browsers, Safari, and `curl` trust the routeup CA from the system store automatically. Some language runtimes ship their own CA bundle and ignore the system store — point them at the routeup CA:

```bash
export REQUESTS_CA_BUNDLE=~/.routeup/ca.crt   # Python (requests / urllib3)
export NODE_EXTRA_CA_CERTS=~/.routeup/ca.crt  # Node.js
```

Firefox uses its own trust store too: import `~/.routeup/ca.crt` under Settings → Privacy & Security → Certificates → View Certificates → Authorities.

## Inspirations

`routeup` draws from several tools that have solved adjacent problems well. None of them are the same shape; each contributes one idea.

- **[Portless](https://portless.dev)** — local-first developer story. Stable HTTPS hostnames for local services, no port juggling, transparent integration with Node scripts.
- **[localtunnel](https://github.com/localtunnel/localtunnel)** — friction-free, token-less, ephemeral public URLs. `routeup`'s public namespace (`try.routeup.dev`) follows this model.
- **[ngrok](https://ngrok.com)** — what a polished public-tunnel CLI feels like. Request inspect/replay UX sets the bar for what `routeup` aims at long-term.
- **[Tailscale Funnel](https://tailscale.com/kb/1223/funnel)** — identity-as-namespace. Funnel binds public hostnames to tailnet identity; `routeup` binds them to token allow patterns. Same shape: who you are determines what you can claim.
- **[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)** — wildcard TLS via ACME DNS-01 and persistent named tunnels. The TLS automation pattern is what the `routeup` server uses.
- **[inlets](https://github.com/inlets/inlets)** / **[frp](https://github.com/fatedier/frp)** — WebSocket + yamux stream multiplexing as the public-tunnel protocol.

`routeup` is not a replacement for any one of these. It combines local-first ergonomics with self-hostable public exposure under one CLI.

## Docs

- [PLAN.md](PLAN.md) — product decisions, constraints, library choices
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — system design, request flows, IPC, conflict resolution
- [docs/MILESTONES.md](docs/MILESTONES.md) — implementation phases
- [docs/ENGINEERING-STANDARDS.md](docs/ENGINEERING-STANDARDS.md) — code quality rules
- [docs/OPEN-QUESTIONS.md](docs/OPEN-QUESTIONS.md) — unresolved design questions, tracked by id
- [AGENTS.md](AGENTS.md) — how AI agents work in this repo

## License

[MIT](LICENSE)
