#!/usr/bin/env bash
# Linux integration check: routeup setup -> serve -> curl over real HTTPS.
#
# Usage: integration-linux.sh <routeup-binary> <tls-port>
#   tls-port 443    exercises the privileged-bind path (setcap on the binary)
#   tls-port >=1024 skips setcap and exercises only trust + agent
#
# Proves: the CA installs into the distro trust store, the agent binds the
# chosen TLS port, and a CA-signed leaf for *.localhost verifies without -k.
set -euo pipefail

BIN=$(realpath "${1:-./bin/routeup}")
PORT="${2:-443}"

WORK=$(mktemp -d)
echo "routeup-ci-ok" >"$WORK/index.html"

HTTP_PID=""
SERVE_PID=""
cleanup() {
	echo "--- serve log ---"
	cat /tmp/routeup-serve.log 2>/dev/null || true
	echo "--- upstream log ---"
	cat /tmp/routeup-http.log 2>/dev/null || true
	[ -n "$SERVE_PID" ] && kill "$SERVE_PID" 2>/dev/null || true
	[ -n "$HTTP_PID" ] && kill "$HTTP_PID" 2>/dev/null || true
	"$BIN" uninstall --yes >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== routeup setup --port $PORT =="
"$BIN" setup --port "$PORT"

echo "== routeup doctor =="
"$BIN" doctor

echo "== upstream http.server on :8080 =="
(cd "$WORK" && exec python3 -m http.server 8080) >/tmp/routeup-http.log 2>&1 &
HTTP_PID=$!
sleep 1

echo "== routeup serve ciapp --port 8080 =="
(cd "$WORK" && exec "$BIN" serve ciapp --port 8080) >/tmp/routeup-serve.log 2>&1 &
SERVE_PID=$!
sleep 3

echo "== curl https://ciapp.localhost:$PORT =="
OUT=$(curl -fsS --resolve "ciapp.localhost:$PORT:127.0.0.1" "https://ciapp.localhost:$PORT/")
echo "$OUT"
echo "$OUT" | grep -q "routeup-ci-ok"

echo "PASS"
