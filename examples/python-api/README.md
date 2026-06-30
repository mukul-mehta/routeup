# Python API Exposure Example

This example demonstrates a single API target with public exposure limited to
webhook paths.

```txt
https://python-api.localhost/*              -> Python API on 127.0.0.1:8082
https://python-api.<namespace>/api/webhooks/* -> public when exposed
```

The local route serves all paths. The `expose.paths` config limits public traffic
to `/api/webhooks/*`.

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
../../routeup serve --expose
```
