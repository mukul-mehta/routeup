set shell := ["bash", "-cu"]

binary := "routeup"
pkg    := "./..."
devel_bin := env("ROUTEUP_DEVEL_BIN", env("HOME") + "/.local/bin/routeup-devel")
devel_state_dir := env("ROUTEUP_DEVEL_STATE_DIR", justfile_directory() + "/.routeup-devel")
devel_tls_port := env("ROUTEUP_DEVEL_TLS_PORT", "47444")

# Default: list recipes
default:
    @just --list

# Run all tests
test:
    go test {{pkg}}

# Run all tests with the race detector
test-race:
    go test -race {{pkg}}

# Run the build-tagged real-dev-server integration tests (needs node + npm +
# network; spins up real Vite and Next dev servers). Excluded from `test`.
test-integration:
    go test -tags integration -run TestIntegration -timeout 15m ./internal/server

# Validate runnable examples and their routeup.json files
test-examples:
    go test ./examples/...

# Run golangci-lint
lint:
    golangci-lint run

# Format Go sources (gofmt + goimports via golangci-lint formatters)
fmt:
    golangci-lint fmt

# Build the routeup binary into ./bin/
build:
    mkdir -p bin
    go build -o bin/{{binary}} ./cmd/routeup

# Rebuild/install routeup-devel and refresh its isolated, trusted profile
install-devel:
    @mkdir -p "$(dirname "{{devel_bin}}")"
    @version="0.0.0-devel+$(git rev-parse --short HEAD)"; if [[ -n "$(git status --porcelain)" ]]; then version="$version.dirty"; fi; go build -ldflags "-X 'github.com/mukul-mehta/routeup/internal/certs.caCommonName=routeup devel local CA' -X github.com/mukul-mehta/routeup/internal/certs.trustFileName=routeup-devel-ca.crt -X github.com/mukul-mehta/routeup/internal/state.defaultDir={{devel_state_dir}} -X github.com/mukul-mehta/routeup/internal/cli.version=$version" -o "{{devel_bin}}" ./cmd/routeup
    @mkdir -p "{{devel_state_dir}}" && chmod 0700 "{{devel_state_dir}}"
    "{{devel_bin}}" setup --no-bind --port "{{devel_tls_port}}" --server=https://edge.routeup.dev --token=

# Stop and remove routeup-devel plus its isolated profile
uninstall-devel:
    @if [[ -x "{{devel_bin}}" ]]; then "{{devel_bin}}" uninstall --yes && rm -f "{{devel_bin}}"; else printf '%s\n' "development binary not found: {{devel_bin}}"; fi

# Fast contributor checks; excludes network-heavy and OS integration tests
check: test-race lint test-examples

# Local equivalent of the portable CI checks (Linux/Fedora jobs remain in Actions)
ci: check test-integration
