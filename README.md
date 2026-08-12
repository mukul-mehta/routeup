# routeup

[![CI](https://github.com/mukul-mehta/routeup/actions/workflows/ci.yml/badge.svg)](https://github.com/mukul-mehta/routeup/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mukul-mehta/routeup)](https://github.com/mukul-mehta/routeup/releases/latest)

`routeup` gives local services stable HTTPS route names and can expose those
same routes publicly through a self-hosted or shared server.

Use it for local apps, webhook testing, OAuth callbacks, mobile device testing,
browser/agent testing, and public preview URLs — no port juggling, no signup
needed for local use.

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

> macOS: the binary is unsigned. `brew` and the `curl` installer work fine;
> only a manual Releases-page download is quarantined — clear it with
> `xattr -d com.apple.quarantine ./routeup`.

## Setup

One-time. Creates a local CA, adds it to your OS trust store, and binds port 443:

```bash
routeup setup
```

You'll be asked for Touch ID or your password once. No sudo is needed after that.
Setup exits nonzero if a requested trust or privileged-port change fails and
does not record an unusable setup marker. To skip privileged port setup, choose
an explicit high port: `routeup setup --no-bind --port 8443`.

Check everything is healthy at any time:

```bash
routeup doctor
```

## Serve a local app

```bash
routeup serve myapp --port 3000      # https://myapp.localhost
routeup serve api.myapp --port 8080  # https://api.myapp.localhost
```

No config file needed for the basic case. Routes are named, stable, and trusted by the system CA.

Open an active route or emit machine-readable/QR output:

```bash
routeup serve myapp --port 3000 --json
routeup serve myapp --port 3000 --qr
```

`--json` writes one ready event after the route is usable. `--qr` prefers the
public URL when exposure is enabled; a local `.localhost` QR only works on the
machine running routeup.

## Runner mode

If your project has a `routeup.json` or a `"routeup"` block in `package.json`,
bare `routeup` starts your development command, assigns a port, and wires up the
environment automatically:

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

```bash
pnpm dev
```

Your app receives:

```txt
PORT              — assigned loopback port
HOST              — 127.0.0.1
ROUTEUP_LOCAL_URL — https://myapp.localhost
ROUTEUP_URL       — https://myapp.localhost  (same as local until expose.enabled)
```

The route is registered before the app starts and unregistered when the process
exits. Your command must honor `PORT` and `HOST`.

Most frameworks that respect `PORT` (Next.js, Nuxt, Express) work with no
changes. Vite and Astro need one line of config — see
[Framework setup](https://routeup.dev/docs/configuration/frameworks).

`name` is optional — if omitted, routeup uses the working-directory basename
(e.g. running from a directory called `myapp` gives `https://myapp.localhost`
automatically).

For editor validation and completion, reference the committed JSON Schema from
`routeup.json`:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "port": 3000
}
```

## Expose publicly

### On demand (standalone)

While a local route is registered, expose it from a second terminal:

```bash
routeup expose                          # reuses the active route's target
routeup expose myapp --port 8080        # explicit port
routeup expose --random                 # random name
routeup expose --qr                     # public URL plus terminal QR code
routeup expose --json                   # ready event for scripts/editors
```

What you get depends on your token:

```txt
with a token:     https://myapp.<your-namespace>.routeup.dev   # persistent
without a token:  https://<label>.try.routeup.dev               # session-only
```

Set your server and token:

```bash
export ROUTEUP_SERVER=https://edge.routeup.dev
export ROUTEUP_TOKEN=sk_routeup_...
```

Or save them permanently:

```bash
routeup setup --server https://edge.routeup.dev
routeup setup --server none             # clear the saved server and token
```

Remote public servers must use HTTPS. Plain HTTP is accepted only for loopback
testing, and a saved token is reused only with the server it was saved for.

### Integrated exposure (`expose.enabled`)

Add `expose.enabled: true` to your config to make `routeup serve` publish the
route without `--expose`. Bare runner mode also claims the public route
**before** launching the child process:

```json
{
  "routeup": {
    "name": "myapp",
    "script": "dev:app",
    "expose": {
      "enabled": true
    }
  }
}
```

```bash
ROUTEUP_SERVER=https://edge.routeup.dev ROUTEUP_TOKEN=sk_routeup_... pnpm dev
```

routeup prints both URLs once the app is ready:

```txt
local:  https://myapp.localhost
public: https://myapp.<your-namespace>.routeup.dev
```

`ROUTEUP_LOCAL_URL` stays local. `ROUTEUP_URL` holds the public address. No
second terminal needed — the tunnel releases with the process.
Foreground commands restore their route and public exposure if the local agent
restarts or a tunnel terminates unexpectedly.

## Frontend + API

Configure multiple path targets behind one route:

```json
{
  "name": "myapp",
  "targets": [
    { "path": "/", "port": 5173 },
    { "path": "/api", "port": 8080 }
  ],
  "expose": {
    "paths": ["/api/*"]
  }
}
```

```bash
routeup serve --expose
```

Local `.localhost` routing serves all targets. `expose.paths` limits which paths
go through the public tunnel.

## Request logs and inspect

```bash
routeup logs myapp                   # recent traffic
routeup logs myapp --follow          # interactive live viewer in a terminal
routeup logs myapp --follow --plain  # streaming lines instead of the viewer
routeup logs myapp --public --json   # public traffic as JSON
routeup logs myapp --since 10m --method POST --status 202 --limit 50
```

Enable opt-in capture for webhook debugging:

```json
{
  "capture": {
    "request": true,
    "response": true,
    "redact_headers": ["authorization", "x-webhook-signature"]
  }
}
```

Then inspect individual exchanges:

```bash
routeup inspect req_Ap7kQ3mN8vR2xLzC
routeup inspect req_Ap7kQ3mN8vR2xLzC --json
```

The default report escapes control and binary bytes so captured traffic cannot
write terminal control sequences. `--raw` restores unescaped retained values
and is unsafe for direct terminal output; `--json` emits byte-exact bodies as
base64. Captured data lives in agent memory only (256 KiB per captured request
or response, 1024-entry ring). It resets when the agent restarts. Capture is off
by default.

Interactive terminal output uses color and emphasis while redirected output
stays plain. Set `NO_COLOR=1` to disable styling. JSON and NDJSON output never
contains terminal formatting.

## Other commands

```bash
routeup routes      # list active local routes (PUBLIC annotation when exposed)
routeup doctor      # check CA, OS trust, port 443, and agent health
routeup config      # show the discovered file and resolved non-secret settings
routeup update      # self-update (use brew upgrade for Homebrew installs)
routeup uninstall   # remove the CA, port-443 helper, and ~/.routeup
```

`routes`, `doctor`, `agent status`, `config`, and `inspect` support `--json`.
`logs --json` remains NDJSON and writes no human messages to stdout.

## Shell completions

`routeup completion` generates a script for your shell. One-time install:

```bash
# Zsh
routeup completion zsh > "${fpath[1]}/_routeup"

# Bash
routeup completion bash > ~/.local/share/bash-completion/completions/routeup

# Fish
routeup completion fish > ~/.config/fish/completions/routeup.fish
```

Subcommands and flags complete statically. When the agent is running,
`routeup logs <Tab>` completes active route names and
`routeup inspect <Tab>` completes captured request IDs.

See [Shell completions](https://routeup.dev/docs/cli-reference/completion) for
PowerShell and full install instructions.

## Non-browser clients

Browsers and `curl` trust the routeup CA from the system store automatically.
Some runtimes ship their own CA bundle:

```bash
export REQUESTS_CA_BUNDLE=~/.routeup/ca.crt   # Python (requests / urllib3)
export NODE_EXTRA_CA_CERTS=~/.routeup/ca.crt  # Node.js (runner mode sets this automatically)
```

Firefox: Settings → Privacy & Security → Certificates → View Certificates →
Authorities → import `~/.routeup/ca.crt`.

## Examples

Runnable examples live in [`examples/`](examples/):

| Example | What it shows |
|---|---|
| `node-basic` | Simplest config: one fixed port, `routeup serve` |
| `node-runner` | Runner mode: bare `routeup` starts the app, injects `PORT`/`HOST`/`ROUTEUP_*` |
| `node-runner-expose` | Runner mode + `expose.enabled: true` — public URL in `ROUTEUP_URL` before child launch |
| `go-split` | Path routing: frontend at `/` and API at `/api` behind one route |
| `python-api` | Webhook debugging: `capture.request`, `redact_headers`, `expose.paths` |

## Docs

- [PLAN.md](PLAN.md) — product decisions, constraints, library choices
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — system design, request flows, IPC, conflict resolution
- [docs/MILESTONES.md](docs/MILESTONES.md) — implementation phases
- [docs/ENGINEERING-STANDARDS.md](docs/ENGINEERING-STANDARDS.md) — code quality rules
- [docs/OPEN-QUESTIONS.md](docs/OPEN-QUESTIONS.md) — unresolved design questions
- [docs/RECOVERY.md](docs/RECOVERY.md) — Fly volume backup and server recovery
- [docs/RELEASING.md](docs/RELEASING.md) — immutable release checklist and verification
- [routeup.schema.json](routeup.schema.json) — editor schema for `routeup.json`
- [AGENTS.md](AGENTS.md) — how AI agents work in this repo

## Inspirations

- **[Portless](https://portless.dev)** — local-first developer story. Stable HTTPS hostnames, no port juggling, transparent Node script integration.
- **[localtunnel](https://github.com/localtunnel/localtunnel)** — friction-free ephemeral public URLs. `routeup`'s public namespace follows this model.
- **[ngrok](https://ngrok.com)** — what a polished tunnel CLI feels like. The request inspection UX sets the bar.
- **[Tailscale Funnel](https://tailscale.com/kb/1223/funnel)** — identity-as-namespace. `routeup` binds public hostnames to token allow patterns, same shape.
- **[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)** — wildcard TLS via ACME DNS-01. The pattern the `routeup` server uses.
- **[inlets](https://github.com/inlets/inlets)** / **[frp](https://github.com/fatedier/frp)** — WebSocket + yamux stream multiplexing as the tunnel protocol.

## Implementation status

Phases 0–10 are complete. Phase 8.5 runner exposure is complete:
`expose.enabled` is implemented and the runner owns the full public tunnel
lifecycle. Framework-specific command adapters are out of scope.

<details>
<summary>Phase checklist</summary>

- [x] Phase 0 — Documentation
- [x] Phase 1 — Scaffolding & walking skeleton
- [x] Phase 2 — Route names & config discovery
- [x] Phase 3 — Local agent on a high port
- [x] Phase 4 — Real local setup (local CA, HTTPS on 443)
- [x] Phase 4.5 — Packaging & lifecycle
- [x] Phase 5 — Public server, tokens & tunnel
- [x] Phase 6 — Streaming, WebSockets, SSE
- [x] Phase 7 — Path proxy
- [x] Phase 8 — Local process runner
- [x] Phase 8.5 — Runner exposure (`expose.enabled`; framework adapters out of scope)
- [x] Phase 9 — Route logs
- [x] Phase 10 — Request capture & inspect

</details>

Planned post-v1 milestones:

- [ ] Phase 11 — Public server rate limiting
- [ ] Phase 12 — Public-route protection
- [ ] Phase 13 — Request replay
- [ ] Phase 14 — `routeup init`
- [ ] Phase 15 — Local TUI and web dashboard
- [ ] Phase 16 — mDNS/LAN mobile mode

## LLM Usage

Most of the groundwork was written by hand. LLMs were used to generate and
review code and documentation.
Harness: [OpenCode](https://opencode.ai/);
Models: GPT-5.6, Claude Opus/Sonnet and GLM 5.2.

- Most tests are LLM-generated across phases
- Phase 6 was fully generated by LLMs
- Phase 8: process runner, examples, and the Linux integration workflow
- Phase 9 and 10: Logs and capture flows
- All examples and documentation were initially written by LLMs and reviewed by me

## License

[MIT](LICENSE)
