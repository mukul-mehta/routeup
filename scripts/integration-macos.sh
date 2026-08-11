#!/usr/bin/env bash
# Safe macOS smoke check: CA generation, detached agent, local TLS routing,
# JSON status, restart reconciliation, and owner cleanup. It never installs CA
# trust, binds a privileged port, runs uninstall, or touches global machine state.
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
	printf '%s\n' "integration-macos.sh requires macOS" >&2
	exit 1
fi

BIN=$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "${1:-./bin/routeup}")
WORK=$(mktemp -d "${TMPDIR:-/tmp}/routeup-macos.XXXXXX")
export HOME="$WORK/home"
export ROUTEUP_AGENT_SOCKET="$WORK/agent.sock"
unset ROUTEUP_NAME ROUTEUP_PORT ROUTEUP_SERVER ROUTEUP_TOKEN
mkdir -p "$HOME" "$WORK/app"
printf '%s\n' "routeup-macos-ok" >"$WORK/app/index.html"

HTTP_PID=""
SERVE_PID=""

cleanup() {
	status=$?
	trap - EXIT
	set +e
	[[ -z "$SERVE_PID" ]] || kill -TERM "$SERVE_PID" 2>/dev/null
	[[ -z "$HTTP_PID" ]] || kill -TERM "$HTTP_PID" 2>/dev/null
	[[ -z "$SERVE_PID" ]] || wait "$SERVE_PID" 2>/dev/null
	[[ -z "$HTTP_PID" ]] || wait "$HTTP_PID" 2>/dev/null
	"$BIN" agent stop >/dev/null 2>&1 || true
	if [[ "$status" -ne 0 ]]; then
		for log in "$WORK/serve.log" "$WORK/http.log" "$HOME/.routeup/agent.log"; do
			printf '%s\n' "--- $log ---"
			[[ ! -f "$log" ]] || cat "$log"
		done
	fi
	rm -rf "$WORK"
	exit "$status"
}
trap cleanup EXIT

free_port() {
	python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

TLS_PORT=$(free_port)
APP_PORT=$(free_port)
while [[ "$APP_PORT" == "$TLS_PORT" ]]; do
	APP_PORT=$(free_port)
done

"$BIN" setup \
	--no-start \
	--no-trust \
	--no-bind \
	--port "$TLS_PORT" \
	--server= \
	--token=

(
	cd "$WORK/app"
	exec python3 -m http.server "$APP_PORT" --bind 127.0.0.1
) >"$WORK/http.log" 2>&1 &
HTTP_PID=$!

"$BIN" agent start
"$BIN" doctor

(
	cd "$WORK/app"
	exec "$BIN" serve macsmoke --port "$APP_PORT" --json
) >"$WORK/serve.log" 2>&1 &
SERVE_PID=$!

probe() {
	curl --fail --silent --show-error \
		--cacert "$HOME/.routeup/ca.crt" \
		--noproxy '*' \
		--resolve "macsmoke.localhost:$TLS_PORT:127.0.0.1" \
		"https://macsmoke.localhost:$TLS_PORT/"
}

ready=false
for _ in {1..60}; do
	if response=$(probe 2>/dev/null) && [[ "$response" == *"routeup-macos-ok"* ]]; then
		ready=true
		break
	fi
	if ! kill -0 "$SERVE_PID" 2>/dev/null; then
		printf '%s\n' "routeup serve exited before becoming ready" >&2
		exit 1
	fi
	sleep 0.2
done
[[ "$ready" == true ]]

"$BIN" routes --json >"$WORK/routes.json"
python3 - "$WORK/routes.json" <<'PY'
import json, pathlib, sys
routes = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert any(route["name"] == "macsmoke" for route in routes)
PY

"$BIN" agent restart
restored=false
for _ in {1..60}; do
	if response=$(probe 2>/dev/null) && [[ "$response" == *"routeup-macos-ok"* ]]; then
		restored=true
		break
	fi
	sleep 0.2
done
[[ "$restored" == true ]]

kill -TERM "$SERVE_PID"
wait "$SERVE_PID" || true
SERVE_PID=""
sleep 0.5

"$BIN" routes --json >"$WORK/routes-after-stop.json"
python3 - "$WORK/routes-after-stop.json" <<'PY'
import json, pathlib, sys
routes = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert not any(route["name"] == "macsmoke" for route in routes)
PY

printf '%s\n' "macOS integration: PASS"
