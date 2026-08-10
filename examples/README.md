# routeup Examples

Runnable examples covering the main routeup config shapes, from a single port to
path-routed targets and public runner exposure. Run commands from inside an
example directory so routeup discovers its `routeup.json` or package.json
`routeup` block.

## Examples

| Example | What it shows |
|---|---|
| [`node-basic`](node-basic/) | Simplest case: one route, one fixed port, `routeup serve` |
| [`node-runner`](node-runner/) | Runner mode: bare `routeup` starts the app, injects `PORT`/`HOST`/`ROUTEUP_*` |
| [`node-runner-expose`](node-runner-expose/) | Runner + `expose.enabled: true`: `ROUTEUP_URL` becomes the granted public URL |
| [`go-split`](go-split/) | Path routing: frontend at `/` and API at `/api` behind one route |
| [`python-api`](python-api/) | Webhook debugging: `capture.request`, `capture.redact_headers`, `expose.paths` |

## Prerequisites

Build routeup from the repository root:

```bash
go build -o ./routeup ./cmd/routeup
```

Run one-time setup if you haven't already:

```bash
./routeup setup
```

## How each example runs

**node-basic, go-split, python-api** — start the app in one terminal and
`../../routeup serve` in another. The app listens on a fixed port; routeup
registers the route and proxies traffic.

**node-runner** — bare `routeup` owns the app process and route lifecycle.
No second terminal needed:

```bash
cd examples/node-runner
PATH="$(cd ../.. && pwd):$PATH" pnpm dev
```

**node-runner-expose** — same as node-runner but `expose.enabled: true` in
the config. routeup claims a public route before launching the app and tears
it down on exit. Requires `ROUTEUP_SERVER` and `ROUTEUP_TOKEN`:

```bash
cd examples/node-runner-expose
ROUTEUP_SERVER=https://routeup.dev ROUTEUP_TOKEN=sk_routeup_... \
  PATH="$(cd ../.. && pwd):$PATH" pnpm dev
```

## Tests

Example configs and Node/Python syntax are covered by `go test ./examples/...`,
included in the normal test run:

```bash
go test ./examples/...
```
