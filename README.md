# routeup

`routeup` gives local services stable HTTPS route names and can expose those same routes publicly when needed.

It is an open source developer tool for local apps, APIs, webhooks, OAuth callbacks, mobile testing, and browser/agent testing.

## Status

In planning. No implementation yet.

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
