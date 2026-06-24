// Package tunnel implements the routeup request tunnel: a WebSocket connection
// from the local agent to the public server, multiplexed with yamux so each
// public HTTP request becomes one stream.
// Roles: the agent dials and is the yamux client; the public server accepts and
// is the yamux server. The agent opens a single control stream to assert its
// route claims; the server opens one stream per inbound public request, which
// the agent serves against its local handler.
//
// This file holds the wire protocol shared by both ends. server.go is the
// server-side registry; client.go is the agent-side client.
//
// Tunnel session lifecycle (HTTP is carried over yamux streams; net/http does
// the wire work on each stream, yamux just multiplexes the bytes):
//
//	Agent                                   Server
//	─────                                   ──────
//	WebSocket dial  ───────────────────────► /_routeup/tunnel: upgrade
//	yamux.Client(conn)                       yamux.Server(conn)
//	open stream 0 (control) ───────────────► accept stream 0
//	send HandshakeMessage{claim, spec} ──────────► broker.Hold(spec):
//	                                           authorize → HoldRoute → ensure cert
//	   HandshakeMessage{claim_ok, host} ◄────────── register host → reverse proxy
//
//	── tunnel established; per inbound public request: ──
//
//	http.Server.Serve(session)               serveIngress → ReverseProxy
//	accept stream N ◄──────────────────────── http.Transport dials session.Open()
//	  read request, proxy to localhost:port     (writes the request onto stream N)
//	  write response onto stream N ──────────► http.Transport reads the response,
//	                                             streams it to the public client
//
//	── agent disconnects: control stream EOF → server releases the hold (grace) ──
package tunnel

import (
	"context"
)

const (
	Version       = "routeup-tunnel/1"
	VersionHeader = "Routeup-Tunnel-Version"
	Path          = "/_routeup/tunnel"
)

const (
	msgClaim    = "claim"
	msgClaimOK  = "claim_ok"
	msgClaimErr = "claim_err"
)

// HandshakeMessage is the JSON message exchanged on the control stream (stream 0).
// One route per session: the agent sends Claim; the server replies with the
// resolved Granted host, or an Error (+HTTP-style Code) on rejection.
type HandshakeMessage struct {
	Type    string     `json:"type"`
	Claim   *ClaimSpec `json:"claim,omitempty"`
	Granted string     `json:"granted,omitempty"`
	Error   string     `json:"error,omitempty"`
	Code    int        `json:"code,omitempty"`
}

type ClaimSpec struct {
	Route string `json:"route"`
}

// RouteBroker authorizes and holds the route an agent claims, for the lifetime
// of its tunnel session. The tunnel package owns no policy or storage: when an
// agent connects and sends its ClaimSpec, the registry calls Hold (which the
// server implements as authorize + persist + ensure-cert) and Release when the
// session ends. Hold returns the resolved public host.
type RouteBroker interface {
	Hold(ctx context.Context, token string, spec ClaimSpec) (host string, err error)
	Release(host string)
}

type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }
