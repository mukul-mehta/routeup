# routeup

`routeup` gives local services stable HTTPS route names and can expose those same routes publicly when needed.

It is an open source developer tool for local apps, APIs, webhooks, OAuth callbacks, mobile testing, and browser/agent testing.

## Status

Phase 2 — Route names & config discovery. `routeup serve <name> --port <p> --dry-run` resolves the route and prints local + public URLs. No real networking yet.

## Implementation Progress

Currently: Phase 2 complete — route parser, single-dir config discovery, flag/env/config precedence, `expose --dry-run`.

Phase definitions and acceptance criteria live in [docs/MILESTONES.md](docs/MILESTONES.md).

- [x] **Phase 0 — Documentation:** PLAN, README, ARCHITECTURE, ENGINEERING-STANDARDS, MILESTONES, OPEN-QUESTIONS, AGENTS, LICENSE
- [x] **Phase 1 — Scaffolding & walking skeleton:** Go module, lint, CI, cobra root with `doctor`/`routes`/`logs` stubs
- [x] **Phase 2 — Route names & config discovery:** parser, hostname mapping, dry-run expose
- [ ] **Phase 3 — Local agent on a high port:** registry, CLI↔agent IPC, reverse proxy by Host
- [ ] **Phase 4 — Process runner:** child process with `PORT`/`HOST`/`ROUTEUP_*` env injection
- [ ] **Phase 5 — Real local setup:** local CA, certificate generation, HTTPS on 443
- [ ] **Phase 6 — Public server & tokens:** route claim API, token allow patterns, public namespace
- [ ] **Phase 7 — Tunnel MVP:** WebSocket + yamux, one public request reaches a local port
- [ ] **Phase 8 — Streaming, WebSockets, SSE:** real dev servers work through the tunnel
- [ ] **Phase 9 — Path proxy:** frontend + API behind one route
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
