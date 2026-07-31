set shell := ["bash", "-cu"]

binary := "routeup"
pkg    := "./..."

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

# Dev loop: go run with positional args (e.g. `just dev doctor`)
dev *args:
    @go run ./cmd/routeup {{args}}

# Local equivalent of the deterministic CI checks
ci: test-race lint

# Local equivalent of the examples workflow
ci-examples: test-examples

# Local equivalent of the real-dev-server integration workflow
ci-integration: test-integration
