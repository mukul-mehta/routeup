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
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// This file is the agent side of the tunnel. It dials the public server, claims
// one route, then serves the public requests the server pushes back down the
// same connection by proxying each to a local handler.
//
// Run(ctx) is a reconnect loop around one session, split into two halves:
//
//	Run(ctx)
//	  └─ connectAndServe(ctx)
//	       ├─ handshake(ctx)   the initial connect — runs once, returns fast
//	       └─ serve(ctx)       the steady state    — blocks for the session
//	     transient failure → back off (500ms→30s) and loop
//	     PermanentError (claim rejected) or ctx cancelled → return
//
// handshake — the "connect" half:
//
//  1. dial wss://<server>/_routeup/tunnel
//     (headers: Routeup-Tunnel-Version, Authorization: Bearer <token>)
//  2. yamux.Client(conn): a WebSocket is ONE ordered byte stream; yamux layers
//     many independent streams over it. The agent is the yamux client (it dials
//     and opens the control stream); the server is the yamux server.
//  3. open stream 0 — the control stream — and send HandshakeMessage{type:claim, spec}.
//  4. read the reply on stream 0:
//     claim_ok  → onGranted(host). That callback is how the agent's Expose
//     hands the granted host back to the CLI and returns, while
//     THIS goroutine falls through into serve() and keeps running.
//     claim_err → PermanentError (retrying would get the same answer).
//
// serve — the steady state: the server opens one new yamux stream per inbound
// public request. A yamux session is a net.Listener, so serve hands it straight
// to a standard http.Server — no hand-rolled accept loop:
//
//	http.Server.Serve(session)   // session.Accept() yields one stream per request
//
//	  net/http reads the HTTP request off each stream, runs handler.ServeHTTP
//	  (the local reverse proxy to localhost:<port>), and writes the response back
//	  on the same stream; framing, flushing, and concurrency are the stdlib's job.
//
// So onGranted fires once at the boundary between the two halves: the handshake
// is done, the host goes back to the caller, and serve() keeps the tunnel alive
// until ctx is cancelled (CLI Unexpose, owner death, agent shutdown) or the
// session drops. One session multiplexes the control stream plus one stream per
// concurrent public request.
//
// Client is the agent-side tunnel client. It dials the public server, claims
// one route, and serves inbound request streams against a local handler.
type Client struct {
	serverURL string
	token     string
	spec      ClaimSpec
	handler   http.Handler
	logger    *slog.Logger
	onGranted func(string)
}

// ClientOptions configures a Client.
type ClientOptions struct {
	ServerURL string
	Token     string
	Spec      ClaimSpec
	Handler   http.Handler
	Logger    *slog.Logger
	// OnGranted, if set, is called with the resolved public host once the
	// session is established.
	OnGranted func(host string)
}

// NewClient builds a Client.
func NewClient(opts ClientOptions) *Client {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Client{
		serverURL: opts.ServerURL,
		token:     opts.Token,
		spec:      opts.Spec,
		handler:   opts.Handler,
		logger:    logger,
		onGranted: opts.OnGranted,
	}
}

// Run keeps one tunnel session alive. Network/session failures are retried with
// backoff; claim rejections are permanent because retrying would produce the
// same server answer.
func (c *Client) Run(ctx context.Context) error {
	backoff := baseBackoff
	for {
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var perm *PermanentError
		if errors.As(err, &perm) {
			return err
		}
		c.logger.Warn("tunnel disconnected, retrying", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectAndServe runs one session: the handshake (connect + claim), then serve
// (handle request streams) for as long as the session lasts.
func (c *Client) connectAndServe(ctx context.Context) error {
	session, ctrl, cleanup, err := c.handshake(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return c.serve(ctx, session, ctrl)
}

// handshake is the initial connect: dial the WebSocket, layer yamux over it,
// open the control stream (stream 0), send the claim, and wait for the grant.
// On success it returns the live session, the open control stream, and a
// cleanup func the caller must defer (it closes the session and the underlying
// WebSocket). A claim rejection comes back as a *PermanentError.
func (c *Client) handshake(ctx context.Context) (yamuxSession *yamux.Session, ctrlStream net.Conn, cleanup func(), err error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	header := http.Header{}
	header.Set(VersionHeader, Version)
	header.Set("Authorization", "Bearer "+c.token)

	wsConn, _, err := websocket.Dial(dialCtx, c.wsURL(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial tunnel: %w", err)
	}
	wsConn.SetReadLimit(-1)

	// A WebSocket is one ordered byte stream; yamux multiplexes many streams
	// over it. The agent dials, so it is the yamux client.
	conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	session, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		_ = wsConn.Close(websocket.StatusNormalClosure, "")
		return nil, nil, nil, fmt.Errorf("yamux client: %w", err)
	}
	cleanup = func() {
		_ = session.Close()
		_ = wsConn.Close(websocket.StatusNormalClosure, "")
	}

	// Stream 0 is the control stream: claim the route, then wait for the granted
	// public host before serve() starts accepting request streams.
	ctrl, err := session.OpenStream()
	if err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("open control stream: %w", err)
	}
	if err := writeHandshakeMessage(ctrl, HandshakeMessage{Type: msgClaim, Claim: &c.spec}); err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("send claim: %w", err)
	}
	var reply HandshakeMessage
	if err := json.NewDecoder(ctrl).Decode(&reply); err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("read claim reply: %w", err)
	}
	if reply.Type != msgClaimOK {
		cleanup()
		return nil, nil, nil, &PermanentError{Err: fmt.Errorf("claim rejected: %s", reply.Error)}
	}
	if c.onGranted != nil {
		c.onGranted(reply.Granted)
	}
	c.logger.Info("tunnel established", "host", reply.Granted)
	return session, ctrl, cleanup, nil
}

// serve turns the yamux session into an HTTP server for the session's lifetime.
//
// A yamux session is a net.Listener whose Accept yields one stream per inbound
// public request (the server opens them). So we hand the session straight to a
// standard http.Server: it reads each request off its stream, runs it through
// c.handler (the local reverse proxy to localhost:<port>), and serializes the
// response back onto the stream — Content-Length/chunked framing, flushing, and
// concurrency all handled by the standard library. yamux just carries the bytes.
//
// It blocks until the session ends (server dropped it → retry in Run) or ctx is
// cancelled (Unexpose / owner death / agent shutdown). On cancel we close the
// control stream — signalling the server we're gone — and the session, which
// makes Serve return.
func (c *Client) serve(ctx context.Context, session *yamux.Session, ctrl net.Conn) error {
	srv := &http.Server{
		Handler:           c.handler,
		ReadHeaderTimeout: requestHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(c.logger.Handler(), slog.LevelDebug),
	}

	// Tear down on ctx cancel; also stop this watcher when Serve returns on its
	// own (the server dropped the session), via the deferred cancel.
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-serveCtx.Done()
		_ = ctrl.Close()
		_ = srv.Close()
		_ = session.Close()
	}()

	err := srv.Serve(session) // session satisfies net.Listener
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) wsURL() string {
	u := c.serverURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return strings.TrimRight(u, "/") + Path
}
