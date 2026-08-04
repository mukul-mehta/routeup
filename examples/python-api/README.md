# Python API Exposure Example

This example demonstrates a single API target with public exposure limited to
webhook paths.

```txt
https://python-api.localhost/*              -> Python API on 127.0.0.1:8082
https://python-api.<namespace>.routeup.dev/api/webhooks/* -> public when exposed
```

The local route serves all paths. The `expose.paths` config limits public traffic
to `/api/webhooks/*`. This example enables `capture: true`, so it is also the
recommended manual test for `routeup inspect`.

## Run

Terminal 1:

```bash
cd examples/python-api
python3 app.py
```

Terminal 2:

```bash
cd examples/python-api
../../routeup serve
```

Open or test locally:

```bash
curl https://python-api.localhost/api/healthz
```

If you have a routeup server configured, expose only webhook paths:

```bash
../../routeup expose
```

Run that in a third terminal while the local `serve` command remains active. It
reuses the registered target and applies the configured `expose.paths`.

## Request Capture And Inspect

With the app and local route running, send a webhook-shaped request:

```bash
curl \
  -4 \
  -X POST \
  -H 'X-Routeup-Capture: local-example' \
  -H 'Content-Type: application/json' \
  --data '{"event":"local-example"}' \
  https://python-api.localhost/api/webhooks/demo
```

The response includes the forwarded header and body byte count. Then list local
logs and inspect the request ID from the final column:

On macOS, `-4` reaches routeup's loopback forwarder at `127.0.0.1:443`. The
current forwarder does not listen on IPv6 loopback.

```bash
../../routeup logs python-api --local
../../routeup inspect req_<request-id>
```

Inspect should show the original `X-Routeup-Capture` header, JSON body, target
`/:8082`, and `Complete: true`.

For public traffic, start `../../routeup expose`, copy its printed public URL,
and post the same request to `<public-url>/api/webhooks/demo`. Then run:

```bash
../../routeup logs python-api --public
../../routeup inspect req_<request-id>
```

The inspected entry should have `Source: public` and the same retained request
data. A request to a public path outside `/api/webhooks/*` returns 404 and is not
forwarded to this app.
