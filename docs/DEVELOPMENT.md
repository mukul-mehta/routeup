# Local Development

Routeup uses one isolated development profile for CLI, agent, TLS, and route
behavior. It creates and trusts a separate development CA:

```bash
just install-devel
routeup-devel dashboard
just uninstall-devel
```

The defaults are:

```txt
binary:    ~/.local/bin/routeup-devel
state:     <repository>/.routeup-devel
TLS port:  47444
CA:        separate trusted "routeup devel local CA"
port 443:  untouched
```

Every `just install-devel` invocation rebuilds and overwrites the stable
development binary, reruns setup, and starts or restarts the agent when the
binary changed. The profile has its own socket, PID, log, CA, setup marker, and
saved client configuration. It does not replace `HOME`, so child processes
retain the developer's normal home directory.

Development versions identify the source commit and uncommitted state, for
example `0.0.0-devel+ba5851f.dirty`.

The development build embeds its selected state path, so after setup it can be
used directly from `PATH` without exporting anything:

```bash
routeup-devel dashboard
routeup-devel routes
routeup-devel logs --follow
```

The development CA is trusted during installation, so browsers and command-line
clients need no additional certificate arguments:

```bash
curl https://myapp.localhost:47444/
```

`just uninstall-devel` stops the isolated agent, removes the development CA from
the trust store, deletes its state, and removes the `routeup-devel` binary. It
never removes normal routeup trust, the global port helper, or Linux
capabilities.

Override the defaults when parallel profiles or another port are needed:

```bash
ROUTEUP_DEVEL_BIN="$HOME/.local/bin/routeup-feature" \
ROUTEUP_DEVEL_STATE_DIR="$PWD/.routeup-feature" \
ROUTEUP_DEVEL_TLS_PORT=47445 \
just install-devel
```

At runtime, `ROUTEUP_STATE_DIR` can override the state path embedded in the
development binary. An explicit `ROUTEUP_AGENT_SOCKET` still takes precedence
for the IPC socket.

## Privileged System Test

Only use the real setup path when changing CA trust, port 443, the macOS
LaunchDaemon, or Linux capabilities:

```bash
just build
./bin/routeup setup
./bin/routeup doctor
```

This intentionally changes machine-wide setup. On macOS, setup copies the
current binary to a root-owned helper path under `/Library/PrivilegedHelperTools`
and points the LaunchDaemon there; rebuilding `routeup-devel` cannot replace code
executed as root. After testing, rerun setup from the installed release binary
to refresh the helper and restore the release setup marker:

```bash
routeup setup
```

Setup markers use the initial version 1 format; no migration code exists.
`routeup doctor` rejects missing or malformed markers and invalid macOS helper
arguments, including configurations without IPv6 forwarding. It also verifies
root ownership, non-writable permissions, and that the helper matches the binary
recorded by setup.

## Verification

Fast contributor checks:

```bash
just check
```

Isolated macOS integration harness:

```bash
just smoke-macos
```

The smoke harness uses a temporary state tree and dynamic ports. It never
changes trust, port 443, or global service configuration.
