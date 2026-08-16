#!/usr/bin/env bash
# Safe macOS integration check: CA generation, detached agent, local TLS routing,
# runner lifecycle, restart reconciliation, and owner cleanup. It never installs
# CA trust, binds a privileged port, runs uninstall, or touches global machine state.
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
	printf '%s\n' "integration-macos.sh requires macOS" >&2
	exit 1
fi

BIN=$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "${1:-./bin/routeup}")
SCRIPT_DIR=$(python3 -c 'import os,sys; print(os.path.dirname(os.path.abspath(sys.argv[1])))' "${BASH_SOURCE[0]}")
WORK=$(mktemp -d "${TMPDIR:-/tmp}/routeup-macos.XXXXXX")
export HOME="$WORK/home"
export ROUTEUP_AGENT_SOCKET="$WORK/agent.sock"
export ROUTEUP_RUNNER_FIXTURE="$SCRIPT_DIR/integration-runner.py"
unset ROUTEUP_NAME ROUTEUP_PORT ROUTEUP_SERVER ROUTEUP_TOKEN ROUTEUP_STATE_DIR
mkdir -p "$HOME" "$WORK/app" "$WORK/runner"
printf '%s\n' "routeup-macos-ok" >"$WORK/app/index.html"

HTTP_PID=""
SERVE_PID=""
RUNNER_PID=""
APP_PID=""
DESCENDANT_PID=""

wait_bounded() {
	local pid="$1"
	local attempts="$2"
	local attempt status
	for ((attempt = 0; attempt < attempts; attempt++)); do
		if ! kill -0 "$pid" 2>/dev/null; then
			break
		fi
		sleep 0.1
	done
	if kill -0 "$pid" 2>/dev/null; then
		kill -KILL "$pid" 2>/dev/null || true
	fi
	status=0
	wait "$pid" || status=$?
	return "$status"
}

stop_and_wait() {
	local pid="$1"
	if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
		kill -TERM "$pid" 2>/dev/null || true
	fi
	if [[ -n "$pid" ]]; then
		wait_bounded "$pid" 80 >/dev/null 2>&1 || true
	fi
}

cleanup() {
	status=$?
	trap - EXIT
	set +e
	stop_and_wait "$RUNNER_PID"
	stop_and_wait "$SERVE_PID"
	stop_and_wait "$HTTP_PID"
	[[ -z "$APP_PID" ]] || kill -KILL "$APP_PID" 2>/dev/null || true
	[[ -z "$DESCENDANT_PID" ]] || kill -KILL "$DESCENDANT_PID" 2>/dev/null || true
	"$BIN" agent stop >/dev/null 2>&1 || true
	if [[ "$status" -ne 0 ]]; then
		for log in "$WORK/serve.log" "$WORK/http.log" "$WORK/runner.log" "$HOME/.routeup/agent.log"; do
			printf '%s\n' "--- $log ---"
			[[ ! -f "$log" ]] || cat "$log"
		done
	fi
	rm -rf "$WORK"
	exit "$status"
}
trap cleanup EXIT

route_row() {
	local wanted="$1"
	local routes="$2"
	local line name
	while IFS= read -r line; do
		read -r name _ <<<"$line"
		if [[ "$name" == "$wanted" ]]; then
			printf '%s\n' "$line"
			return 0
		fi
	done <<<"$routes"
	return 1
}

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
	curl --fail --silent --show-error --connect-timeout 5 --max-time 5 \
		--cacert "$HOME/.routeup/ca.crt" \
		--noproxy '*' \
		--resolve "macsmoke.localhost:$TLS_PORT:127.0.0.1" \
		"https://macsmoke.localhost:$TLS_PORT/"
}

wait_for_https() {
	local host="$1"
	local path="$2"
	local output="$3"
	local watched_pid="${4:-}"
	local attempt
	for ((attempt = 0; attempt < 60; attempt++)); do
		if curl --fail --silent --show-error --connect-timeout 5 --max-time 5 \
			--cacert "$HOME/.routeup/ca.crt" --noproxy '*' \
			--resolve "$host:$TLS_PORT:127.0.0.1" \
			"https://$host:$TLS_PORT$path" >"$output" 2>/dev/null; then
			return 0
		fi
		if [[ -n "$watched_pid" ]] && ! kill -0 "$watched_pid" 2>/dev/null; then
			return 1
		fi
		sleep 0.2
	done
	return 1
}

wait_for_exit() {
	local pid="$1"
	local attempt
	for ((attempt = 0; attempt < 50; attempt++)); do
		if ! kill -0 "$pid" 2>/dev/null; then
			return 0
		fi
		sleep 0.1
	done
	return 1
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

cat >"$WORK/runner/routeup.json" <<'JSON'
{
  "name": "runnerci",
  "command": "exec python3 \"$ROUTEUP_RUNNER_FIXTURE\""
}
JSON

EXPECTED_RUNNER_URL="https://runnerci.localhost:$TLS_PORT"

# Pre-warm: run the fixture once directly from bash before the routeup runner
# starts its 15-second startup clock. On macOS, the first execution of a Python
# script may trigger an OS-level security scan (XProtect/Gatekeeper) that adds
# several seconds of latency; running it here ensures that check completes
# before the Go timer begins.
_WARMUP_PORT=$(free_port)
mkdir -p "$WORK/fixture-warmup"
(
	cd "$WORK/fixture-warmup"
	HOST=127.0.0.1 PORT="$_WARMUP_PORT" \
		ROUTEUP_LOCAL_URL="$EXPECTED_RUNNER_URL" \
		ROUTEUP_URL="$EXPECTED_RUNNER_URL" \
		exec python3 "$ROUTEUP_RUNNER_FIXTURE"
) >"$WORK/fixture-warmup.log" 2>&1 &
_WARMUP_PID=$!
_warmup_bound=false
for ((attempt = 0; attempt < 50; attempt++)); do
	if nc -z 127.0.0.1 "$_WARMUP_PORT" 2>/dev/null; then
		_warmup_bound=true
		break
	fi
	sleep 0.1
done
kill -KILL "$_WARMUP_PID" 2>/dev/null || true
wait "$_WARMUP_PID" 2>/dev/null || true
if [[ "$_warmup_bound" != true ]]; then
	printf '%s\n' "runner fixture pre-warm failed — python3 or fixture may be broken" >&2
	cat "$WORK/fixture-warmup.log" >&2
	exit 1
fi

printf '%s\n' "== bare routeup runner =="
(
	cd "$WORK/runner"
	unset ROUTEUP_NAME ROUTEUP_PORT PORT HOST ROUTEUP_LOCAL_URL ROUTEUP_URL
	exec "$BIN"
) >"$WORK/runner.log" 2>&1 &
RUNNER_PID=$!

if ! wait_for_https "runnerci.localhost" "/env" "$WORK/runner-response.txt" "$RUNNER_PID"; then
	printf '%s\n' "runner did not become reachable" >&2
	exit 1
fi

cat "$WORK/runner-response.txt"
RUNNER_ENV=()
while IFS= read -r line; do
	RUNNER_ENV+=("$line")
done <"$WORK/runner-response.txt"
if [[ "${RUNNER_ENV[0]:-}" != "routeup-runner-ok" ]]; then
	printf '%s\n' "runner response marker is missing" >&2
	exit 1
fi

APP_PORT="${RUNNER_ENV[1]:-}"
APP_PORT="${APP_PORT#PORT=}"
if ! [[ "$APP_PORT" =~ ^[0-9]+$ ]] || [[ "$APP_PORT" == "$TLS_PORT" ]]; then
	printf '%s\n' "runner assigned invalid dynamic port: $APP_PORT" >&2
	exit 1
fi
if [[ "${RUNNER_ENV[2]:-}" != "HOST=127.0.0.1" ]] || \
	[[ "${RUNNER_ENV[3]:-}" != "ROUTEUP_LOCAL_URL=$EXPECTED_RUNNER_URL" ]] || \
	[[ "${RUNNER_ENV[4]:-}" != "ROUTEUP_URL=$EXPECTED_RUNNER_URL" ]]; then
	printf '%s\n' "runner environment did not match expected values" >&2
	exit 1
fi

for file in app.pid app.pgid descendant.pid descendant.pgid; do
	for ((attempt = 0; attempt < 50; attempt++)); do
		if [[ -s "$WORK/runner/$file" ]]; then
			break
		fi
		sleep 0.1
	done
	if [[ ! -s "$WORK/runner/$file" ]]; then
		printf '%s\n' "runner fixture did not write $file" >&2
		exit 1
	fi
done

APP_PID=$(<"$WORK/runner/app.pid")
APP_PGID=$(<"$WORK/runner/app.pgid")
DESCENDANT_PID=$(<"$WORK/runner/descendant.pid")
DESCENDANT_PGID=$(<"$WORK/runner/descendant.pgid")
if [[ "$APP_PGID" != "$APP_PID" ]] || [[ "$DESCENDANT_PGID" != "$APP_PID" ]]; then
	printf '%s\n' "runner did not own one child process group" >&2
	exit 1
fi

"$BIN" routes --json >"$WORK/runner-routes.json"
python3 - "$WORK/runner-routes.json" "$RUNNER_PID" "$WORK/runner" "$APP_PORT" <<'PY'
import json
import pathlib
import sys

routes = json.loads(pathlib.Path(sys.argv[1]).read_text())
pid = int(sys.argv[2])
runner_dir = str(pathlib.Path(sys.argv[3]).resolve())
port = int(sys.argv[4])
runner = next(route for route in routes if route["name"] == "runnerci")
if runner["targets"] != [{"path": "/", "port": port}]:
    raise AssertionError(f"runner targets = {runner['targets']!r}")
if runner["owner_pid"] != pid:
    raise AssertionError(f"runner owner pid = {runner['owner_pid']}, want {pid}")
if str(pathlib.Path(runner["owner_cwd"]).resolve()) != runner_dir:
    raise AssertionError(f"runner cwd = {runner['owner_cwd']!r}, want {runner_dir!r}")
if "public_host" in runner:
    raise AssertionError(f"runner unexpectedly public: {runner!r}")
PY

printf '%s\n' "== stop runner and verify cleanup =="
kill -TERM "$RUNNER_PID"
RUNNER_STATUS=0
wait_bounded "$RUNNER_PID" 150 || RUNNER_STATUS=$?
RUNNER_PID=""
if [[ "$RUNNER_STATUS" -ne 42 ]]; then
	printf '%s\n' "runner exit status = $RUNNER_STATUS, want 42" >&2
	exit 1
fi
if [[ "$(<"$WORK/runner/app.signal")" != "SIGTERM" ]] || \
	[[ "$(<"$WORK/runner/descendant.signal")" != "SIGTERM" ]]; then
	printf '%s\n' "runner did not forward SIGTERM to the full process group" >&2
	exit 1
fi
if ! wait_for_exit "$APP_PID" || ! wait_for_exit "$DESCENDANT_PID"; then
	printf '%s\n' "runner left a child process alive" >&2
	exit 1
fi
APP_PID=""
DESCENDANT_PID=""

ROUTES=$("$BIN" routes)
if route_row "runnerci" "$ROUTES" >/dev/null; then
	printf '%s\n' "runner route remained registered after exit" >&2
	exit 1
fi
if ! route_row "macsmoke" "$ROUTES" >/dev/null; then
	printf '%s\n' "runner cleanup removed an unrelated route" >&2
	exit 1
fi

RUNNER_NOT_FOUND="$WORK/runner-not-found.txt"
NOT_FOUND_STATUS=$(curl --silent --show-error --output "$RUNNER_NOT_FOUND" --write-out '%{http_code}' \
	--cacert "$HOME/.routeup/ca.crt" --noproxy '*' \
	--resolve "runnerci.localhost:$TLS_PORT:127.0.0.1" \
	"https://runnerci.localhost:$TLS_PORT/env")
if [[ "$NOT_FOUND_STATUS" != "404" ]] || \
	! grep -q "reason: no route is currently registered for runnerci" "$RUNNER_NOT_FOUND"; then
	printf '%s\n' "former runner route did not return the expected 404" >&2
	exit 1
fi

kill -TERM "$SERVE_PID"
wait_bounded "$SERVE_PID" 80 >/dev/null 2>&1 || true
SERVE_PID=""
sleep 0.5

"$BIN" routes --json >"$WORK/routes-after-stop.json"
python3 - "$WORK/routes-after-stop.json" <<'PY'
import json, pathlib, sys
routes = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert not any(route["name"] == "macsmoke" for route in routes)
PY

printf '%s\n' "macOS integration: PASS"
