# Routeup configuration patterns

Use project configuration when the route should be repeatable or when routeup
should start the application. For a one-off existing process, flags are enough.

Routeup checks only the current working directory. It reads `routeup.json` or a
`routeup` block in `package.json`; `routeup.json` takes precedence when both are
present. Do not add both formats for the same project.

## Fixed port

Use this when another command owns the application process:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "port": 3000
}
```

Then run `routeup serve` from that directory.

## Routeup runner

Non-JavaScript projects can put a shell command in `routeup.json`:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "command": "go run ./cmd/dev"
}
```

JavaScript projects select a package script from the `package.json` routeup
block. Do not point `script` back to a script that runs bare `routeup`:

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

Run bare `routeup` directly or through the package's `dev` script. The child
must honor the injected `HOST` and `PORT`. If a framework ignores them, configure
that framework explicitly rather than hard-coding another port.

## Multiple targets

Route the longest matching path prefix to its target:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "targets": [
    { "path": "/", "port": 5173 },
    { "path": "/api", "port": 8080 }
  ]
}
```

Use one `routeup serve` owner for the route. Start supporting processes with
their normal commands or `routeup exec`; do not have each process claim the same
route.

## Integrated public exposure

Only add `expose.enabled` when public exposure should happen every time the
configured route starts:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "port": 3000,
  "expose": {
    "enabled": true
  }
}
```

Limit public traffic to selected paths when appropriate. Local routing still
serves all configured targets:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "targets": [
    { "path": "/", "port": 5173 },
    { "path": "/api", "port": 8080 }
  ],
  "expose": {
    "enabled": true,
    "paths": ["/api/webhooks/*"]
  }
}
```

An `expose` object containing only `paths` constrains standalone exposure; it
does not automatically expose runner or serve mode.

## Request capture

Capture is disabled by default. Enable only the required direction and redact
all credentials, cookies, and webhook signatures relevant to the application:

```json
{
  "$schema": "https://raw.githubusercontent.com/mukul-mehta/routeup/main/routeup.schema.json",
  "name": "myapp",
  "port": 3000,
  "capture": {
    "request": true,
    "response": false,
    "redact_headers": [
      "authorization",
      "cookie",
      "x-webhook-signature"
    ]
  }
}
```

Capture retains bounded data in the local agent's in-memory request ring. It is
not a reason to expose the route publicly; local requests can also be captured.
