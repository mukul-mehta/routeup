import http from "node:http";

const port = 5174;

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/time") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ now: new Date().toISOString(), host: req.headers.host }, null, 2) + "\n");
    return;
  }

  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>routeup node basic example</title>
    <style>
      body { margin: 0; font-family: system-ui, sans-serif; background: #0f172a; color: #f8fafc; }
      main { max-width: 680px; margin: 12vh auto; padding: 32px; }
      code { background: #1e293b; border-radius: 8px; padding: 2px 6px; }
      .card { background: #172033; border: 1px solid #334155; border-radius: 18px; padding: 24px; }
    </style>
  </head>
  <body>
    <main>
      <div class="card">
        <p>Node.js single-target app</p>
        <h1>https://node-basic.localhost</h1>
        <p>This route uses <code>{ "port": 5174 }</code>, the shorthand for a single root target.</p>
        <p>Try <a href="/time">/time</a>.</p>
      </div>
    </main>
  </body>
</html>`);
});

server.listen(port, "127.0.0.1", () => {
  console.log(`app listening on http://127.0.0.1:${port}`);
  console.log("run `../../routeup serve` in this directory, then open https://node-basic.localhost");
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
