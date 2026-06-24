package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// This file is the server side of the tunnel: it accepts the agent's outbound
// WebSocket, runs the claim handshake, then forwards public requests back down
// the same connection. The HTTP roles are inverted from the transport roles —
// the agent dialed, but the server is the HTTP *client* on each request.
//
//	AcceptHandler — the HTTP handler mounted at /_routeup/tunnel:
//	  └─ verify version header + read the Bearer token (from the UPGRADE request,
//	     which is real HTTP; the control stream below is plain JSON, not HTTP)
//	  └─ websocket.Accept → ServeConn(conn, token)   // blocks for the session
//
//	ServeConn — one agent tunnel, start to finish:
//	  1. yamux.Server(conn): server takes the yamux server role (the agent dialed)
//	  2. accept stream 0 (control); decode HandshakeMessage{claim, spec}
//	  3. broker.Hold(token, spec): authorize + persist + ensure cert → public host
//	     (or a coded error → claim_err, return)
//	  4. register(host, newSessionProxy(session)): store host → reverse proxy
//	  5. reply HandshakeMessage{claim_ok, host} on stream 0
//	  6. io.Copy(io.Discard, ctrl): stream 0 stays open as the liveness signal;
//	     its EOF means the agent disconnected → release(host) (grace window)
//
//	Per inbound public request (steady state):
//	  serveIngress → Handler(host) → the session's ReverseProxy.ServeHTTP, whose
//	  http.Transport.DialContext = session.Open → a NEW yamux stream; net/http
//	  writes the request and reads the response over it. This mirrors the agent's
//	  http.Server.Serve(session): net/http does the HTTP wire work on each stream,
//	  yamux just multiplexes the bytes.
//
// register stores an http.Handler (a proxy bound to one session), not a raw
// session, so ingress stays a plain Handler(host).ServeHTTP and the "open a
// stream on this agent" logic is sealed inside the proxy.

// TunnelRegistry is the server-side registry of live agent tunnels. It accepts
// agent sessions and, for each claimed public host, keeps a reverse proxy that
// carries public requests to that host's session. serveIngress dispatches to it
// by Host.
type TunnelRegistry struct {
	broker RouteBroker
	logger *slog.Logger

	mu     sync.RWMutex
	routes map[string]http.Handler // public host -> reverse proxy over its session
}

// NewTunnelRegistry returns a registry backed by broker.
func NewTunnelRegistry(broker RouteBroker, logger *slog.Logger) *TunnelRegistry {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &TunnelRegistry{
		broker: broker,
		logger: logger,
		routes: make(map[string]http.Handler),
	}
}

// AcceptHandler returns the HTTP handler for the tunnel endpoint. It checks the
// protocol version, upgrades to WebSocket, and serves the session until it ends.
func (reg *TunnelRegistry) AcceptHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(VersionHeader) != Version {
			http.Error(w, "tunnel protocol version mismatch", http.StatusBadRequest)
			return
		}
		token := bearerToken(r.Header.Get("Authorization"))

		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// Disable coder/websocket's default read limit: tunnelled bodies are
		// arbitrary size and yamux frames ride this connection.
		c.SetReadLimit(-1)

		conn := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
		err = reg.ServeConn(r.Context(), conn, token)
		if err != nil {
			reg.logger.Debug("tunnel session ended", "err", err)
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
}

// ServeConn runs one tunnel session over the agent's upgraded WebSocket. token
// is the bearer token from the upgrade request. It runs the claim handshake on
// the control stream, registers the route, then BLOCKS for the whole session.
//
// Why block: while ServeConn parks here, the session — and its host -> proxy
// registration — stays alive, which is what lets public requests (handled on
// other goroutines) keep opening streams to this agent. It returns only when the
// agent disconnects (control stream EOF) or the server shuts down (ctx cancel);
// on return the deferred release frees the route and the hold (grace window).
//
// Two phases: handshake (one-time) and session lifetime (the block).
func (reg *TunnelRegistry) ServeConn(ctx context.Context, conn net.Conn, token string) error {
	// The agent dialed, so the server takes the yamux *server* role. From here
	// either side can open streams over this one connection.
	session, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		return fmt.Errorf("yamux server: %w", err)
	}
	defer func() { _ = session.Close() }()

	// --- handshake (one-time) ---

	// Stream 0 (control): the agent opens it first, so AcceptStream blocks only
	// briefly. It carries plain-JSON HandshakeMessages — not HTTP.
	ctrl, err := session.AcceptStream()
	if err != nil {
		return fmt.Errorf("accept control stream: %w", err)
	}

	// Read the single claim message; reject anything that isn't a valid claim.
	var msg HandshakeMessage
	if err := json.NewDecoder(ctrl).Decode(&msg); err != nil {
		return fmt.Errorf("decode claim: %w", err)
	}
	if msg.Type != msgClaim || msg.Claim == nil || msg.Claim.Route == "" {
		_ = writeHandshakeMessage(ctrl, HandshakeMessage{Type: msgClaimErr, Error: "expected a claim message with a route"})
		return errors.New("first control message was not a valid claim")
	}

	// Authorize + persist + ensure cert, via the RouteBroker. Returns the public
	// host the server granted, or a coded error (401/403/409) relayed verbatim.
	host, err := reg.broker.Hold(ctx, token, *msg.Claim)
	if err != nil {
		_ = writeHandshakeMessage(ctrl, HandshakeMessage{Type: msgClaimErr, Error: err.Error(), Code: statusCodeOf(err)})
		return err
	}

	// Make the host serviceable: store host -> a reverse proxy bound to THIS
	// session, so serveIngress can route public requests to it. The deferred
	// release (on return) removes the route and frees the hold (grace window).
	reg.register(host, newSessionProxy(session, host, reg.logger))
	defer reg.release(host)

	// Grant: tell the agent its host; its handshake unblocks and it begins
	// serving request streams.
	if err := writeHandshakeMessage(ctrl, HandshakeMessage{Type: msgClaimOK, Granted: host}); err != nil {
		return err
	}
	reg.logger.Info("tunnel session established", "host", host)

	// --- session lifetime (until disconnect) ---

	// Server-shutdown path: cancelling ctx closes the session, which unblocks the
	// read below.
	go func() {
		<-ctx.Done()
		_ = session.Close()
	}()
	// Hold the session open for the tunnel's whole life. There's no payload to
	// read (the agent sends nothing more on stream 0); blocking on this read is
	// purely what keeps the session — and the registered proxy — alive so the
	// server can keep opening request streams to the agent. It unblocks only when
	// the control stream EOFs (the agent died/disconnected) or the session is
	// closed by the ctx-cancel goroutine above; either way ServeConn then returns
	// and the deferred release tears the route down.
	_, _ = io.Copy(io.Discard, ctrl)
	reg.logger.Info("tunnel session closed", "host", host)
	return nil
}

// Handler returns the reverse proxy for the live session holding host, if any.
// serveIngress dispatches each public request to it; a missing entry is a 503.
func (reg *TunnelRegistry) Handler(host string) (http.Handler, bool) {
	reg.mu.RLock()
	h, ok := reg.routes[host]
	reg.mu.RUnlock()
	return h, ok
}

// newSessionProxy builds the reverse proxy that carries a public request to the
// agent over its yamux session. The http.Transport's dialer opens a fresh yamux
// stream per request (session.Open); the standard library writes the request and
// reads the response over it. This is the server-side mirror of the agent's
// http.Server: net/http does the HTTP wire work, yamux carries the bytes.
func newSessionProxy(session *yamux.Session, host string, logger *slog.Logger) http.Handler {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return session.Open()
		},
		DisableKeepAlives: true, // one yamux stream per request
	}
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = host   // placeholder; the dialer ignores the addr
			pr.Out.Host = pr.In.Host // preserve the public Host end-to-end
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Warn("tunnel forward failed", "host", host, "err", err)
			http.Error(w, "routeup: tunnel error", http.StatusBadGateway)
		},
	}
}

func (reg *TunnelRegistry) register(host string, h http.Handler) {
	reg.mu.Lock()
	reg.routes[host] = h
	reg.mu.Unlock()
}

func (reg *TunnelRegistry) release(host string) {
	reg.mu.Lock()
	delete(reg.routes, host)
	reg.mu.Unlock()
	reg.broker.Release(host)
}
