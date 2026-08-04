import http from "node:http";

const host = process.env.HOST;
const port = Number(process.env.PORT);

if (!host || !Number.isInteger(port) || port < 1) {
  throw new Error("routeup must provide HOST and PORT");
}

const environment = () => ({
  host,
  port,
  localURL: process.env.ROUTEUP_LOCAL_URL,
  publicURL: process.env.ROUTEUP_URL,
});

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/env") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(`${JSON.stringify(environment(), null, 2)}\n`);
    return;
  }

  const env = environment();
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>routeup runner expose example</title>
    <style>
      body { margin: 0; font-family: system-ui, sans-serif; background: #0f172a; color: #f8fafc; }
      main { max-width: 680px; margin: 12vh auto; padding: 32px; }
      code { background: #1e293b; border-radius: 8px; padding: 2px 6px; }
      .card { background: #172033; border: 1px solid #334155; border-radius: 18px; padding: 24px; }
      .url { margin: 8px 0; }
      .label { color: #94a3b8; font-size: 0.85em; }
    </style>
  </head>
  <body>
    <main>
      <div class="card">
        <p>Node.js runner with expose.enabled</p>
        <h1>routeup runner is active</h1>
        <div class="url">
          <div class="label">local</div>
          <code>${env.localURL ?? "not set"}</code>
        </div>
        <div class="url">
          <div class="label">public</div>
          <code>${env.publicURL ?? "not set"}</code>
        </div>
        <p><a href="/env">Inspect the injected environment as JSON</a></p>
      </div>
    </main>
  </body>
</html>
`);
});

server.listen(port, host, () => {
  console.log(`app listening on http://${host}:${port}`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
