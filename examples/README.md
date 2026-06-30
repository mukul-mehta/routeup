# routeup Examples

These examples demonstrate a few routeup config shapes, from one route/one port
to one route with multiple path targets.

```txt
/      -> frontend target
/api/* -> API target
```

Each example has its own `routeup.json`. Run commands from inside the example
directory so routeup discovers the right config.

## Examples

- [`go-split`](go-split/) - frontend + API behind one route using path targets.
- [`node-basic`](node-basic/) - one Node.js app behind one route using `port`.
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

Then pick an example, start its app process, and run `../../routeup serve` from
the same example directory.

The example configs and dependency-free Node/Python syntax checks are covered by
`go test ./examples/...`, which is included in the normal repository test run.
