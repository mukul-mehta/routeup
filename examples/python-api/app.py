#!/usr/bin/env python3

import json
import signal
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ADDR = ("127.0.0.1", 8082)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/healthz":
            self.write_text("ok\n")
            return
        if self.path == "/api/message":
            self.write_json(
                {
                    "language": "python",
                    "message": "hello from the API target",
                    "host": self.headers.get("host", ""),
                    "path": self.path,
                }
            )
            return
        self.write_text("python-api example\ntry /api/healthz or /api/message\n")

    def do_POST(self):
        if self.path == "/api/webhooks/demo":
            self.write_json({"status": "received", "path": self.path})
            return
        self.send_error(404)

    def write_text(self, text):
        body = text.encode()
        self.send_response(200)
        self.send_header("content-type", "text/plain; charset=utf-8")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def write_json(self, value):
        body = (json.dumps(value, indent=2) + "\n").encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        return


def main():
    server = ThreadingHTTPServer(ADDR, Handler)
    print(f"api listening on http://{ADDR[0]}:{ADDR[1]}")
    print("run `../../routeup serve` in this directory, then test https://python-api.localhost/api/healthz")

    def shutdown(_signum, _frame):
        server.shutdown()
        sys.exit(0)

    signal.signal(signal.SIGINT, shutdown)
    signal.signal(signal.SIGTERM, shutdown)
    server.serve_forever()


if __name__ == "__main__":
    main()
