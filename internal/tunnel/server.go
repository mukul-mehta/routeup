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
	"sync"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// ErrNoSession means no live tunnel currently holds the requested host.
var ErrNoSession = errors.New("no active tunnel for host")

// Hub is the server-side tunnel registry: it accepts agent sessions, tracks
// which live session holds each public host, and forwards public requests to
// the holding session.
type Hub struct {
	keeper ClaimKeeper
	logger *slog.Logger

	mu       sync.RWMutex
	sessions map[string]*yamux.Session // public host -> session
}

// NewHub returns a Hub backed by keeper.
func NewHub(keeper ClaimKeeper, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Hub{
		keeper:   keeper,
		logger:   logger,
		sessions: make(map[string]*yamux.Session),
	}
}

// AcceptHandler returns the HTTP handler for the tunnel endpoint. It checks the
// protocol version, upgrades to WebSocket, and serves the session until it ends.
func (h *Hub) AcceptHandler() http.HandlerFunc {
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
		if err := h.ServeConn(r.Context(), conn, token); err != nil {
			h.logger.Debug("tunnel session ended", "err", err)
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
}

// ServeConn runs a tunnel session over an accepted connection. token is the
// authenticated bearer token. It blocks until the session ends, then releases
// the session's claims.
func (h *Hub) ServeConn(ctx context.Context, conn net.Conn, token string) error {
	session, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		return fmt.Errorf("yamux server: %w", err)
	}
	defer func() { _ = session.Close() }()

	ctrl, err := session.AcceptStream()
	if err != nil {
		return fmt.Errorf("accept control stream: %w", err)
	}

	var msg ControlMsg
	if err := json.NewDecoder(ctrl).Decode(&msg); err != nil {
		return fmt.Errorf("decode claim: %w", err)
	}
	if msg.Type != msgClaim {
		_ = writeControl(ctrl, ControlMsg{Type: msgClaimErr, Error: "expected a claim message"})
		return errors.New("first control message was not a claim")
	}

	hosts, err := h.holdAll(ctx, token, msg.Claims)
	if err != nil {
		_ = writeControl(ctrl, ControlMsg{Type: msgClaimErr, Error: err.Error(), Code: statusCodeOf(err)})
		return err
	}

	h.register(hosts, session)
	defer h.releaseAll(hosts)

	if err := writeControl(ctrl, ControlMsg{Type: msgClaimOK, Granted: hosts}); err != nil {
		return err
	}
	h.logger.Info("tunnel session established", "hosts", hosts)

	// Block until the control stream closes (agent disconnect) or ctx is done.
	go func() {
		<-ctx.Done()
		_ = session.Close()
	}()
	_, _ = io.Copy(io.Discard, ctrl)
	h.logger.Info("tunnel session closed", "hosts", hosts)
	return nil
}

// RoundTrip forwards req to the live session holding host and returns its
// response. The returned response Body owns the underlying stream; closing it
// closes the stream.
func (h *Hub) RoundTrip(host string, req *http.Request) (*http.Response, error) {
	h.mu.RLock()
	session := h.sessions[host]
	h.mu.RUnlock()
	if session == nil {
		return nil, ErrNoSession
	}

	stream, err := session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	if err := req.Write(stream); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(stream), req)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("read response: %w", err)
	}
	resp.Body = &streamBody{ReadCloser: resp.Body, stream: stream}
	return resp, nil
}

// Hosts returns the public hosts with a live session, for diagnostics.
func (h *Hub) Hosts() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.sessions))
	for host := range h.sessions {
		out = append(out, host)
	}
	return out
}

func (h *Hub) holdAll(ctx context.Context, token string, specs []ClaimSpec) ([]string, error) {
	if len(specs) == 0 {
		return nil, errors.New("no routes requested")
	}
	var hosts []string
	for _, spec := range specs {
		host, err := h.keeper.Hold(ctx, token, spec)
		if err != nil {
			for _, held := range hosts {
				h.keeper.Release(held)
			}
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func (h *Hub) register(hosts []string, s *yamux.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, host := range hosts {
		h.sessions[host] = s
	}
}

func (h *Hub) releaseAll(hosts []string) {
	h.mu.Lock()
	for _, host := range hosts {
		delete(h.sessions, host)
	}
	h.mu.Unlock()
	for _, host := range hosts {
		h.keeper.Release(host)
	}
}

// streamBody ties a response body to its yamux stream so the stream is closed
// when the caller closes the body.
type streamBody struct {
	io.ReadCloser
	stream net.Conn
}

func (b *streamBody) Close() error {
	err := b.ReadCloser.Close()
	_ = b.stream.Close()
	return err
}
