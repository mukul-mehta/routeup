# Node.js Runner + Expose Example

This example extends [node-runner](../node-runner/) with `expose.enabled: true`
in the config. Bare `routeup` claims a public route before launching the child,
injects the granted public URL into `ROUTEUP_URL`, and releases both the tunnel
and the route when the process exits.

## Requirements

A server URL and token:

```bash
export ROUTEUP_SERVER=https://routeup.dev
export ROUTEUP_TOKEN=sk_routeup_...
```

Or save them once with `routeup setup --server https://routeup.dev`. Without a
token, the server must have its public namespace enabled; you will get a
session-only URL under `try.routeup.dev`.

## Run

Build routeup from the repository root:

```bash
go build -o ./routeup ./cmd/routeup
```

Then from this directory:

```bash
cd examples/node-runner-expose
ROUTEUP_SERVER=... ROUTEUP_TOKEN=... PATH="$(cd ../.. && pwd):$PATH" pnpm dev
```

routeup prints something like:

```txt
running: node server.mjs

route: node-runner-expose
local: https://node-runner-expose.localhost
public: https://node-runner-expose.mukul.routeup.dev
```

Inspect the injected environment:

```bash
curl https://node-runner-expose.localhost/env
```

`ROUTEUP_LOCAL_URL` is always the local HTTPS address.
`ROUTEUP_URL` is the granted public URL when `expose.enabled` is set.

Press Ctrl-C to stop. The public tunnel, local route, and child process all
release together.
