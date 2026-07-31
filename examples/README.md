# routeup Examples

These examples demonstrate routeup config shapes and runner mode, from one
route/one process to multiple path targets.

```txt
/      -> frontend target
/api/* -> API target
```

Run commands from inside an example directory so routeup discovers its
`routeup.json` or package.json `routeup` block.

## Examples

- [`go-split`](go-split/) - frontend + API behind one route using path targets.
- [`node-basic`](node-basic/) - one Node.js app behind one route using `port`.
- [`node-runner`](node-runner/) - bare `routeup` runs a configured package script and injects its environment.
- [`python-api`](python-api/) - one Python API with `expose.paths` for webhooks.

## Basic Flow

Build routeup from the repository root:

```bash
go build -o ./routeup ./cmd/routeup
```

Run setup once if needed:

```bash
./routeup setup
```

The `go-split`, `node-basic`, and `python-api` examples start their app and
`../../routeup serve` separately. The `node-runner` example uses bare `routeup`
to own both the app process and route lifecycle.

The example configs and dependency-free Node/Python syntax checks are covered by
`go test ./examples/...`, which is included in the normal repository test run.
