import http.server
import os
from pathlib import Path
import signal
import time


def write(name, value):
    Path(name).write_text(f"{value}\n", encoding="utf-8")


required = ("HOST", "PORT", "ROUTEUP_LOCAL_URL", "ROUTEUP_URL")
environment = {name: os.environ[name] for name in required}
write("app.pid", os.getpid())
write("app.pgid", os.getpgrp())


class Handler(http.server.BaseHTTPRequestHandler):
    # Keep per-connection socket timeout short so the keep-alive receive loop
    # doesn't block handle_request() for the default 30 s, which would prevent
    # the outer `while not stopping` loop from checking the SIGTERM flag.
    timeout = 0.1

    def do_GET(self):
        if self.path != "/env":
            self.send_error(404)
            return
        body = "\n".join(
            [
                "routeup-runner-ok",
                f"PORT={environment['PORT']}",
                f"HOST={environment['HOST']}",
                f"ROUTEUP_LOCAL_URL={environment['ROUTEUP_LOCAL_URL']}",
                f"ROUTEUP_URL={environment['ROUTEUP_URL']}",
            ]
        ) + "\n"
        payload = body.encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, _format, *_args):
        pass


# Fork a descendant to simulate a child process. Using fork (not exec)
# avoids a ~35s XProtect/AMFI scan on macOS 26 that blocks exec() and
# makes SIGKILL ineffective until the scan finishes.
child_pid = os.fork()
if child_pid == 0:
    # On macOS 26, CPython's wakeup-fd-based signal delivery can be
    # unreliable in a forked child because the inherited fd is still
    # owned by the parent's event loop. Disabling it forces the
    # interpreter to rely on raw POSIX signal delivery (EINTR) instead.
    try:
        signal.set_wakeup_fd(-1)
    except (ValueError, OSError):
        pass

    def child_stop(signum, _frame):
        write("descendant.signal", signal.Signals(signum).name)
        os._exit(0)

    signal.signal(signal.SIGINT, child_stop)
    signal.signal(signal.SIGTERM, child_stop)
    write("descendant.pid", os.getpid())
    write("descendant.pgid", os.getpgrp())
    while True:
        time.sleep(0.1)

server = http.server.HTTPServer((environment["HOST"], int(environment["PORT"])), Handler)
server.timeout = 0.1
stopping = False


def stop(signum, _frame):
    global stopping
    write("app.signal", signal.Signals(signum).name)
    stopping = True
    # Explicitly deliver the signal to the child in case process-group
    # delivery is delayed (macOS 26 fork + wakeup-fd timing issue).
    try:
        os.kill(child_pid, signum)
    except ProcessLookupError:
        pass


signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)

try:
    while not stopping:
        server.handle_request()
finally:
    server.server_close()
    # Reap the child with a bounded non-blocking wait so the parent never
    # hangs indefinitely. Fall back to SIGKILL if the child stalls.
    for _ in range(30):  # up to 3 seconds
        try:
            if os.waitpid(child_pid, os.WNOHANG)[0] != 0:
                break
        except ChildProcessError:
            break
        time.sleep(0.1)
    else:
        try:
            os.kill(child_pid, signal.SIGKILL)
            os.waitpid(child_pid, 0)
        except (ProcessLookupError, ChildProcessError):
            pass

raise SystemExit(42)
