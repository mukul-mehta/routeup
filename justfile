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
# network; spins up real Vite and Next dev servers). Excluded from `test`/`ci`.
test-integration:
    go test -tags integration -run TestIntegration -timeout 15m ./internal/server

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

# CI pipeline used in GitHub Actions
ci: test-race lint
