package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Client is the agent-side tunnel client. It dials the public server, claims
// its routes, and serves inbound request streams against a local handler.
type Client struct {
	serverURL string
	token     string
	specs     []ClaimSpec
	handler   http.Handler
	logger    *slog.Logger
	onGranted func([]string)
}

// ClientOptions configures a Client.
type ClientOptions struct {
	ServerURL string
	Token     string
	Specs     []ClaimSpec
	Handler   http.Handler
	Logger    *slog.Logger
	// OnGranted, if set, is called with the resolved public hosts each time a
	// session is established.
	OnGranted func(hosts []string)
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
		specs:     opts.Specs,
		handler:   opts.Handler,
		logger:    logger,
		onGranted: opts.OnGranted,
	}
}

// Run keeps one tunnel session alive. Network/session failures are retried with
// backoff; claim rejections are permanent because retrying would produce the
// same server answer.
func (c *Client) Run(ctx context.Context) error {
	const (
		baseBackoff = 500 * time.Millisecond
		maxBackoff  = 30 * time.Second
	)
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

func (c *Client) connectAndServe(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	header := http.Header{}
	header.Set(VersionHeader, Version)
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}

	wsConn, _, err := websocket.Dial(dialCtx, c.wsURL(), &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		return fmt.Errorf("dial tunnel: %w", err)
	}
	wsConn.SetReadLimit(-1)
	defer func() { _ = wsConn.Close(websocket.StatusNormalClosure, "") }()

	conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	session, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		return fmt.Errorf("yamux client: %w", err)
	}
	defer func() { _ = session.Close() }()

	// Stream 0 is control: the client claims routes and waits for the granted
	// public hosts before accepting request streams.
	ctrl, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	if err := writeControl(ctrl, ControlMsg{Type: msgClaim, Claims: c.specs}); err != nil {
		return fmt.Errorf("send claim: %w", err)
	}
	var reply ControlMsg
	if err := json.NewDecoder(ctrl).Decode(&reply); err != nil {
		return fmt.Errorf("read claim reply: %w", err)
	}
	if reply.Type != msgClaimOK {
		return &PermanentError{Err: fmt.Errorf("claim rejected: %s", reply.Error)}
	}
	if c.onGranted != nil {
		c.onGranted(reply.Granted)
	}
	c.logger.Info("tunnel established", "hosts", reply.Granted)

	return c.serve(ctx, session, ctrl)
}

// serve accepts one stream per inbound public HTTP request until the yamux
// session ends or ctx is cancelled.
func (c *Client) serve(ctx context.Context, session *yamux.Session, ctrl net.Conn) error {
	go func() {
		<-ctx.Done()
		_ = ctrl.Close()
		_ = session.Close()
	}()

	var wg sync.WaitGroup
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accept stream: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.serveStream(stream)
		}()
	}
}

// serveStream handles exactly one HTTP request. The server opens a fresh yamux
// stream per public request, so closing the stream is the response EOF.
func (c *Client) serveStream(stream net.Conn) {
	defer func() { _ = stream.Close() }()

	req, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		return
	}
	defer func() { _ = req.Body.Close() }()
	req.RequestURI = ""
	req.RemoteAddr = "tunnel"

	rw := &streamResponseWriter{w: stream, header: make(http.Header)}
	c.handler.ServeHTTP(rw, requestForHandler(req))
	rw.ensureWritten()
}

// requestForHandler restores fields a server-style request needs, since
// ReadRequest produced a client-style request.
func requestForHandler(req *http.Request) *http.Request {
	if req.URL != nil {
		req.URL.Scheme = ""
		req.URL.Host = ""
	}
	return req
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

// streamResponseWriter writes an HTTP/1.1 response to a raw stream. It frames
// the body with "Connection: close": each request gets its own stream, closed
// when the handler returns, so the reader sees the body end at EOF.
type streamResponseWriter struct {
	w      io.Writer
	header http.Header
	wrote  bool
}

func (rw *streamResponseWriter) Header() http.Header { return rw.header }

func (rw *streamResponseWriter) WriteHeader(status int) {
	if rw.wrote {
		return
	}
	rw.wrote = true
	rw.header.Set("Connection", "close")
	_, _ = io.WriteString(rw.w, fmt.Sprintf("HTTP/1.1 %d %s\r\n", status, http.StatusText(status)))
	_ = rw.header.Write(rw.w)
	_, _ = io.WriteString(rw.w, "\r\n")
}

func (rw *streamResponseWriter) Write(p []byte) (int, error) {
	if !rw.wrote {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.w.Write(p)
}

func (rw *streamResponseWriter) ensureWritten() {
	if !rw.wrote {
		rw.WriteHeader(http.StatusOK)
	}
}
