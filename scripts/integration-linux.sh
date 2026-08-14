#!/usr/bin/env bash
# Linux integration check: setup, trusted local HTTPS, serve, and runner mode.
#
# Usage: integration-linux.sh <routeup-binary> <tls-port>
#   tls-port 443    exercises the privileged-bind path (setcap on the binary)
#   tls-port >=1024 skips setcap and exercises only trust + agent
set -euo pipefail

BIN=$(realpath "${1:-./bin/routeup}")
TLS_PORT="${2:-443}"
SCRIPT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")
export ROUTEUP_RUNNER_FIXTURE="$SCRIPT_DIR/integration-runner.py"
unset ROUTEUP_STATE_DIR

WORK=$(mktemp -d)
export HOME="$WORK/home"
export ROUTEUP_AGENT_SOCKET="$WORK/agent.sock"
mkdir -p "$HOME"

UPSTREAM_DIR="$WORK/upstream"
RUNNER_DIR="$WORK/runner"
mkdir -p "$UPSTREAM_DIR" "$RUNNER_DIR"
printf '%s\n' "routeup-ci-ok" >"$UPSTREAM_DIR/index.html"

HTTP_LOG="$WORK/upstream.log"
SERVE_LOG="$WORK/serve.log"
RUNNER_LOG="$WORK/runner.log"
RUNNER_RESPONSE="$WORK/runner-response.txt"
RUNNER_NOT_FOUND="$WORK/runner-not-found.txt"

HTTP_PID=""
SERVE_PID=""
RUNNER_PID=""
APP_PID=""
DESCENDANT_PID=""

stop_and_wait() {
	local pid="$1"
	if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
		kill -TERM "$pid" 2>/dev/null || true
	fi
	if [ -n "$pid" ]; then
		wait_bounded "$pid" 80 >/dev/null 2>&1 || true
	fi
}

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

cleanup() {
	local status=$?
	trap - EXIT
	set +e

	stop_and_wait "$RUNNER_PID"
	stop_and_wait "$SERVE_PID"
	stop_and_wait "$HTTP_PID"

	if [ -n "$APP_PID" ]; then
		kill -KILL "$APP_PID" 2>/dev/null || true
	fi
	if [ -n "$DESCENDANT_PID" ]; then
		kill -KILL "$DESCENDANT_PID" 2>/dev/null || true
	fi

	"$BIN" uninstall --yes >/dev/null 2>&1 || true

	if [ "$status" -ne 0 ]; then
		for log in "$SERVE_LOG" "$HTTP_LOG" "$RUNNER_LOG" "$HOME/.routeup/agent.log"; do
			printf '%s\n' "--- $log ---"
			if [ -f "$log" ]; then
				cat "$log"
			fi
		done
	fi

	rm -rf "$WORK"
	exit "$status"
}
trap cleanup EXIT

trusted_curl() {
	env -u CURL_CA_BUNDLE -u SSL_CERT_FILE -u SSL_CERT_DIR \
		curl --disable --connect-timeout 2 --max-time 5 --noproxy '*' "$@"
}

route_row() {
	local wanted="$1"
	local routes="$2"
	local line name
	while IFS= read -r line; do
		read -r name _ <<<"$line"
		if [ "$name" = "$wanted" ]; then
			printf '%s\n' "$line"
			return 0
		fi
	done <<<"$routes"
	return 1
}

wait_for_https() {
	local host="$1"
	local path="$2"
	local output="$3"
	local watched_pid="${4:-}"
	local attempt
	for ((attempt = 0; attempt < 60; attempt++)); do
		if trusted_curl --connect-timeout 1 --max-time 1 -fsS \
			--resolve "$host:$TLS_PORT:127.0.0.1" \
			"https://$host:$TLS_PORT$path" >"$output" 2>/dev/null; then
			return 0
		fi
		if [ -n "$watched_pid" ] && ! kill -0 "$watched_pid" 2>/dev/null; then
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

printf '%s\n' "== routeup setup --port $TLS_PORT =="
"$BIN" setup --port "$TLS_PORT" --server= --token=

printf '%s\n' "== routeup doctor =="
"$BIN" doctor

printf '%s\n' "== upstream http.server on :8080 =="
(cd "$UPSTREAM_DIR" && exec python3 -m http.server 8080) >"$HTTP_LOG" 2>&1 &
HTTP_PID=$!

printf '%s\n' "== routeup serve ciapp --port 8080 =="
(cd "$UPSTREAM_DIR" && exec "$BIN" serve ciapp --port 8080) >"$SERVE_LOG" 2>&1 &
SERVE_PID=$!

printf '%s\n' "== curl https://ciapp.localhost:$TLS_PORT =="
wait_for_https "ciapp.localhost" "/" "$WORK/serve-response.txt" "$SERVE_PID"
SERVE_RESPONSE=$(<"$WORK/serve-response.txt")
printf '%s\n' "$SERVE_RESPONSE"
if [[ "$SERVE_RESPONSE" != *"routeup-ci-ok"* ]]; then
	printf '%s\n' "serve response did not contain routeup-ci-ok" >&2
	exit 1
fi

cat >"$RUNNER_DIR/routeup.json" <<'JSON'
{
  "name": "runnerci",
  "command": "exec python3 \"$ROUTEUP_RUNNER_FIXTURE\""
}
JSON

if [ "$TLS_PORT" = "443" ]; then
	EXPECTED_RUNNER_URL="https://runnerci.localhost"
else
	EXPECTED_RUNNER_URL="https://runnerci.localhost:$TLS_PORT"
fi

printf '%s\n' "== bare routeup runner =="
(
	cd "$RUNNER_DIR"
	unset ROUTEUP_NAME ROUTEUP_PORT PORT HOST ROUTEUP_LOCAL_URL ROUTEUP_URL
	exec "$BIN"
) >"$RUNNER_LOG" 2>&1 &
RUNNER_PID=$!

if ! wait_for_https "runnerci.localhost" "/env" "$RUNNER_RESPONSE" "$RUNNER_PID"; then
	printf '%s\n' "runner did not become reachable" >&2
	exit 1
fi

cat "$RUNNER_RESPONSE"
mapfile -t RUNNER_ENV <"$RUNNER_RESPONSE"
if [ "${RUNNER_ENV[0]:-}" != "routeup-runner-ok" ]; then
	printf '%s\n' "runner response marker is missing" >&2
	exit 1
fi

APP_PORT="${RUNNER_ENV[1]:-}"
APP_PORT="${APP_PORT#PORT=}"
if ! [[ "$APP_PORT" =~ ^[0-9]+$ ]] || [ "$APP_PORT" = "$TLS_PORT" ] || [ "$APP_PORT" = "8080" ]; then
	printf '%s\n' "runner assigned invalid dynamic port: $APP_PORT" >&2
	exit 1
fi
if [ "${RUNNER_ENV[2]:-}" != "HOST=127.0.0.1" ] || \
	[ "${RUNNER_ENV[3]:-}" != "ROUTEUP_LOCAL_URL=$EXPECTED_RUNNER_URL" ] || \
	[ "${RUNNER_ENV[4]:-}" != "ROUTEUP_URL=$EXPECTED_RUNNER_URL" ]; then
	printf '%s\n' "runner environment did not match expected values" >&2
	exit 1
fi

for file in app.pid app.pgid descendant.pid descendant.pgid; do
	for ((attempt = 0; attempt < 50; attempt++)); do
		if [ -s "$RUNNER_DIR/$file" ]; then
			break
		fi
		sleep 0.1
	done
	if [ ! -s "$RUNNER_DIR/$file" ]; then
		printf '%s\n' "runner fixture did not write $file" >&2
		exit 1
	fi
done

APP_PID=$(<"$RUNNER_DIR/app.pid")
APP_PGID=$(<"$RUNNER_DIR/app.pgid")
DESCENDANT_PID=$(<"$RUNNER_DIR/descendant.pid")
DESCENDANT_PGID=$(<"$RUNNER_DIR/descendant.pgid")
if [ "$APP_PGID" != "$APP_PID" ] || [ "$DESCENDANT_PGID" != "$APP_PID" ]; then
	printf '%s\n' "runner did not own one child process group" >&2
	exit 1
fi

"$BIN" routes --json >"$WORK/runner-routes.json"
python3 - "$WORK/runner-routes.json" "$RUNNER_PID" "$RUNNER_DIR" "$APP_PORT" <<'PY'
import json, pathlib, sys

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
wait_bounded "$RUNNER_PID" 80 || RUNNER_STATUS=$?
RUNNER_PID=""
if [ "$RUNNER_STATUS" -ne 42 ]; then
	printf '%s\n' "runner exit status = $RUNNER_STATUS, want 42" >&2
	exit 1
fi
if [ "$(<"$RUNNER_DIR/app.signal")" != "SIGTERM" ] || \
	[ "$(<"$RUNNER_DIR/descendant.signal")" != "SIGTERM" ]; then
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
if ! route_row "ciapp" "$ROUTES" >/dev/null; then
	printf '%s\n' "runner cleanup removed an unrelated route" >&2
	exit 1
fi

NOT_FOUND_STATUS=$(trusted_curl -sS -o "$RUNNER_NOT_FOUND" -w '%{http_code}' \
	--resolve "runnerci.localhost:$TLS_PORT:127.0.0.1" \
	"https://runnerci.localhost:$TLS_PORT/env")
if [ "$NOT_FOUND_STATUS" != "404" ] || \
	! grep -q "reason: no route is currently registered for runnerci" "$RUNNER_NOT_FOUND"; then
	printf '%s\n' "former runner route did not return the expected 404" >&2
	exit 1
fi

printf '%s\n' "PASS"
