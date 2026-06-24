# syntax=docker/dockerfile:1
#
# Build the routeup server image for Fly.io. Pure-Go (no cgo): modernc.org/sqlite,
# certmagic, coder/websocket, and yamux all build static, so CGO_ENABLED=0 works
# and the runtime image can be distroless/static.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.0.0-fly
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/mukul-mehta/routeup/internal/cli.version=${VERSION}" \
    -o /routeup ./cmd/routeup

# Alpine runtime: tiny, ships /bin/sh (so `fly ssh console` works for minting
# tokens), and ca-certificates for outbound TLS to Let's Encrypt and Cloudflare.
# The binary is static (CGO disabled), so it runs fine on musl. Runs as root,
# which lets it write the mounted /data volume.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /routeup /usr/local/bin/routeup
COPY deploy/routeup-server.json /etc/routeup/server.json
EXPOSE 8443
ENTRYPOINT ["/usr/local/bin/routeup"]
CMD ["server", "--config", "/etc/routeup/server.json"]
