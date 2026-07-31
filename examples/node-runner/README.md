# Node.js Runner Example

This example exercises Phase 8. The package `dev` script starts bare `routeup`,
which resolves `dev:app`, assigns a port, injects the route environment, and
owns the Node process and route registration.

Build routeup from the repository root:

```bash
go build -o ./routeup ./cmd/routeup
```

Then run from this directory:

```bash
cd examples/node-runner
PATH="$(cd ../.. && pwd):$PATH" pnpm dev
```

Set `STARTUP_DELAY_MS=2000` before `pnpm dev` to verify routeup waits for the
target before printing the route as ready.

Once routeup prints the local URL, inspect the child environment:

```bash
curl https://node-runner.localhost/env
```

For this local-only example, `ROUTEUP_LOCAL_URL` and `ROUTEUP_URL` both equal
the printed `https://node-runner.localhost` URL. The assigned `PORT` is dynamic
unless `ROUTEUP_PORT` is set.

Press Ctrl-C in the runner terminal. The Node process should stop and
`node-runner` should disappear from `routeup routes`.
