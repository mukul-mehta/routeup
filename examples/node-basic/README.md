# Node.js Basic Example

This example demonstrates the simplest routeup config: one route, one local
target port.

```txt
https://node-basic.localhost -> Node.js app on 127.0.0.1:5174
```

## Run

Terminal 1:

```bash
cd examples/node-basic
npm start
```

Terminal 2:

```bash
cd examples/node-basic
../../routeup serve
```

Open:

```txt
https://node-basic.localhost
```

Or test directly:

```bash
curl https://node-basic.localhost/time
```
