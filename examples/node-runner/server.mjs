import http from "node:http";

const host = process.env.HOST;
const port = Number(process.env.PORT);
const startupDelay = Number(process.env.STARTUP_DELAY_MS ?? 0);

if (!host || !Number.isInteger(port) || port < 1) {
  throw new Error("routeup must provide HOST and PORT");
}
if (!Number.isFinite(startupDelay) || startupDelay < 0) {
  throw new Error("STARTUP_DELAY_MS must be a non-negative number");
}

const environment = () => ({
  host,
  port,
  localURL: process.env.ROUTEUP_LOCAL_URL,
  routeupURL: process.env.ROUTEUP_URL,
});

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/env") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(`${JSON.stringify(environment(), null, 2)}\n`);
    return;
  }

  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>routeup node runner example</title>
  </head>
  <body>
    <main>
      <h1>routeup runner is active</h1>
      <p><a href="/env">Inspect the injected environment</a></p>
    </main>
  </body>
</html>
`);
});

setTimeout(() => {
  server.listen(port, host, () => {
    console.log(`app listening on http://${host}:${port}`);
  });
}, startupDelay);

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
