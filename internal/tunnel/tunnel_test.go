package tunnel

// This file holds the tunnel package's transport-level tests. They exercise the
// agent-side Client and the server-side TunnelRegistry directly — no real public
// server, SQLite, tokens, or TLS in the way — so they test the tunnel transport
// in isolation: the claim handshake, host granting, request forwarding, and the
// streaming behaviors M6 cares about (WebSocket, SSE, large bodies, slow starts).
//
// Test doubles:
//   - fakeBroker implements tunnel.RouteBroker. It grants
//     "<route>.alice.routeup.dev" for any token except failTok (rejected with a
//     401-coded error) and records which hosts were Held/Released. It stands in
//     for the real server broker (authorize + persist + cert) so these tests
//     never touch SQLite or auth.
//   - codedErr is an error carrying an HTTP status, used to confirm a rejection
//     code travels back to the client inside a PermanentError.
//
// Harness (startPublicTunnel): a realistic tunnel needs TWO HTTP servers, because
// in production the agent dials the server while the public user hits the server
// from the other side:
//   - tunnelServer serves reg.AcceptHandler() — the /_routeup/tunnel endpoint the
//     agent's Client dials and upgrades to a WebSocket.
//   - publicServer looks up reg.Handler(host) and forwards into the held session —
//     what the test's "public client" sends normal HTTP/WS/SSE requests to.
//
// Both are real httptest.NewServer listeners, never httptest.NewRecorder, because
// a WebSocket upgrade needs a real, hijackable connection (a recorder cannot be
// hijacked). The Client runs in a goroutine; OnGranted delivers the granted host
// over a channel so the test knows the claim succeeded and the proxy is live.
//
// Tests:
//   - TestTunnel_EndToEnd: claim a route, then drive one request straight at the
//     registry's per-session proxy and confirm it reaches the backend with Host
//     and path intact. The base case.
//   - TestTunnel_NoSession: Handler(host) reports false when nothing holds host.
//   - TestTunnel_ClaimRejected: a bad token makes connectAndServe return a
//     PermanentError (no retry) carrying the rejection reason.
//   - TestTunnel_ReleaseOnDisconnect: cancelling the Client's context (agent
//     disconnect) makes the server Release the hold (the grace-window trigger).
//   - TestTunnel_WebSocketHMR: a real coder/websocket dial through the tunnel —
//     proves the HTTP/1.1 Upgrade survives the hijack-over-yamux on both hops, the
//     negotiated subprotocol round-trips, the server push (greeting) arrives, and
//     a client message is echoed back. Models Vite HMR.
//   - TestTunnel_SSEStreamsIncrementally: proves the response is NOT buffered to
//     EOF. The backend writes event "one", then blocks on a gate before "two";
//     receiving "one" while "two" is still unwritten is the proof. Models Next-
//     style event streaming.
//   - TestTunnel_LargeBodyEcho: POST 16 MiB (larger than the yamux stream window)
//     through one stream into an echo backend and SHA-256 compare both ways —
//     proves large request+response bodies stream intact under flow control.
//   - TestTunnel_SlowFirstByteStillCompletes: a backend that delays before the
//     first byte still completes, proving there is no short per-request deadline.
//
// Helpers: wsURL rewrites http(s)→ws(s) for dialing; stripTestPort drops the
// :port from a Host so registry lookup matches the granted host; readSSEData (and
// the WithTimeout watchdog wrapper) parse one SSE event's data line so a broken
// stream fails fast instead of hanging; hashReader/hashReaderN SHA-256 a stream
// without buffering it.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mukul-mehta/routeup/internal/streamtest"
)

// fakeBroker grants a host per route and tracks holds/releases.
// Imitates the actual RouteBroker in tunnel/protocol.go
type fakeBroker struct {
	mu            sync.Mutex
	held          map[string]bool
	released      map[string]bool
	mockFailToken string // if token == mockFailToken, Hold rejects, tests failure condition of actual broker
}

func newFakeBroker() *fakeBroker {
	return &fakeBroker{held: map[string]bool{}, released: map[string]bool{}}
}

type codedErr struct {
	msg  string
	code int
}

func (e *codedErr) Error() string   { return e.msg }
func (e *codedErr) StatusCode() int { return e.code }

func (k *fakeBroker) Hold(_ context.Context, token string, spec ClaimSpec) (string, error) {
	if token == k.mockFailToken {
		return "", &codedErr{msg: "invalid token", code: http.StatusUnauthorized}
	}
	host := spec.Route + ".alice.routeup.dev"
	k.mu.Lock()
	k.held[host] = true
	k.mu.Unlock()
	return host, nil
}

func (k *fakeBroker) Release(host string) {
	k.mu.Lock()
	k.released[host] = true
	k.mu.Unlock()
}

func (k *fakeBroker) wasReleased(host string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.released[host]
}

func TestTunnel_EndToEnd(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "hello host=%s path=%s", r.Host, r.URL.Path)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)

	reg := NewTunnelRegistry(newFakeBroker(), nil)
	ts := httptest.NewServer(reg.AcceptHandler())
	defer ts.Close()

	grantedCh := make(chan string, 1)
	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "valid_token",
		Spec:      ClaimSpec{Route: "api.myapp"},
		Handler:   httputil.NewSingleHostReverseProxy(target),
		OnGranted: func(h string) { grantedCh <- h },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	var host string
	select {
	case host = <-grantedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for claim grant")
	}
	if host != "api.myapp.alice.routeup.dev" {
		t.Fatalf("granted = %q", host)
	}

	h, ok := reg.Handler(host)
	if !ok {
		t.Fatalf("no handler registered for %s", host)
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/ping", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "hello host=") || !strings.Contains(string(body), "path=/ping") {
		t.Errorf("body = %q", body)
	}
}

func TestTunnel_NoSession(t *testing.T) {
	reg := NewTunnelRegistry(newFakeBroker(), nil)
	if _, ok := reg.Handler("nobody.routeup.dev"); ok {
		t.Errorf("expected no handler for an unconnected host")
	}
}

func TestTunnel_ClaimRejected(t *testing.T) {
	broker := newFakeBroker()
	broker.mockFailToken = "invalid_token"
	reg := NewTunnelRegistry(broker, nil)
	ts := httptest.NewServer(reg.AcceptHandler())
	defer ts.Close()

	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "invalid_token",
		Spec:      ClaimSpec{Route: "api"},
		Handler:   http.NewServeMux(),
	})
	err := client.connectAndServe(context.Background())
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("err = %v, want PermanentError", err)
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("err = %v", err)
	}
}

func TestTunnel_ReleaseOnDisconnect(t *testing.T) {
	broker := newFakeBroker()
	reg := NewTunnelRegistry(broker, nil)
	ts := httptest.NewServer(reg.AcceptHandler())
	defer ts.Close()

	grantedCh := make(chan string, 1)
	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "valid_token",
		Spec:      ClaimSpec{Route: "api.myapp"},
		Handler:   http.NewServeMux(),
		OnGranted: func(h string) { grantedCh <- h },
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = client.Run(ctx) }()

	var host string
	select {
	case host = <-grantedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no grant")
	}

	cancel() // disconnect the agent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if broker.wasReleased(host) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("hold for %s was not released after disconnect", host)
}

func TestTunnel_WebSocketHMR(t *testing.T) {
	publicURL, host, cleanup := startPublicTunnel(t, streamtest.WSHMR(streamtest.WSOptions{
		Subprotocols: []string{"vite-hmr"},
		Greeting:     "hmr:connected",
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(publicURL, "/hmr"), &websocket.DialOptions{
		Host:         host,
		Subprotocols: []string{"vite-hmr"},
	})
	if err != nil {
		t.Fatalf("dial websocket through tunnel: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	if got := conn.Subprotocol(); got != "vite-hmr" {
		t.Fatalf("subprotocol = %q, want vite-hmr", got)
	}

	typ, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read greeting: %v", err)
	}
	if typ != websocket.MessageText || string(msg) != "hmr:connected" {
		t.Fatalf("greeting = type %v %q, want text hmr:connected", typ, msg)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	typ, msg, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if typ != websocket.MessageText || string(msg) != "echo:ping" {
		t.Fatalf("echo = type %v %q, want text echo:ping", typ, msg)
	}
}

func TestTunnel_SSEStreamsIncrementally(t *testing.T) {
	gate := make(chan struct{})
	publicURL, host, cleanup := startPublicTunnel(t, streamtest.SSEHMR([]string{"one", "two"}, gate))
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, publicURL+"/_next/webpack-hmr", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get sse through tunnel: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	reader := bufio.NewReader(resp.Body)
	first := readSSEDataWithTimeout(t, reader, 2*time.Second)
	if first != "one" {
		t.Fatalf("first SSE event = %q, want one", first)
	}

	// The backend has not been allowed to write the second event yet, so receiving
	// the first event proves the tunnel did not buffer until EOF or stream close.
	gate <- struct{}{}
	second := readSSEDataWithTimeout(t, reader, 2*time.Second)
	if second != "two" {
		t.Fatalf("second SSE event = %q, want two", second)
	}
}

func TestTunnel_LargeBodyEcho(t *testing.T) {
	const size = int64(16 << 20) // 16 MiB: large enough to exceed default windows.

	publicURL, host, cleanup := startPublicTunnel(t, streamtest.EchoBody())
	defer cleanup()

	expected := hashReaderN(t, streamtest.PatternReader(), size)
	req, err := http.NewRequest(http.MethodPost, publicURL+"/upload", io.LimitReader(streamtest.PatternReader(), size))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.ContentLength = size
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("post large body through tunnel: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	actual := hashReader(t, resp.Body)
	if !bytes.Equal(actual, expected) {
		t.Fatalf("large body hash mismatch: got %x want %x", actual, expected)
	}
}

func TestTunnel_SlowFirstByteStillCompletes(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(w, "eventually")
	})
	publicURL, host, cleanup := startPublicTunnel(t, backend)
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, publicURL+"/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("slow response through tunnel: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "eventually" {
		t.Fatalf("status/body = %d %q, want 200 eventually", resp.StatusCode, body)
	}
}

func startPublicTunnel(t *testing.T, backend http.Handler) (publicURL string, host string, cleanup func()) {
	t.Helper()

	reg := NewTunnelRegistry(newFakeBroker(), nil)
	tunnelServer := httptest.NewServer(reg.AcceptHandler())

	grantedCh := make(chan string, 1)
	client := NewClient(ClientOptions{
		ServerURL: tunnelServer.URL,
		Token:     "good",
		Spec:      ClaimSpec{Route: "hmr"},
		Handler:   backend,
		OnGranted: func(h string) { grantedCh <- h },
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- client.Run(ctx) }()

	select {
	case host = <-grantedCh:
	case <-time.After(5 * time.Second):
		cancel()
		tunnelServer.Close()
		t.Fatal("timed out waiting for claim grant")
	}

	publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := reg.Handler(stripTestPort(r.Host))
		if !ok {
			http.Error(w, "no tunnel", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	}))

	cleanup = func() {
		cancel()
		publicServer.Close()
		tunnelServer.Close()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Log("tunnel client did not exit before cleanup timeout")
		}
	}
	return publicServer.URL, host, cleanup
}

func wsURL(httpURL, path string) string {
	u := strings.TrimRight(httpURL, "/") + path
	return "ws://" + strings.TrimPrefix(u, "http://")
}

func stripTestPort(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func readSSEDataWithTimeout(t *testing.T, r *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	ch := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := readSSEData(r)
		if err != nil {
			errCh <- err
			return
		}
		ch <- data
	}()
	select {
	case data := <-ch:
		return data
	case err := <-errCh:
		t.Fatalf("read SSE event: %v", err)
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for SSE event after %s", timeout)
	}
	return ""
}

func readSSEData(r *bufio.Reader) (string, error) {
	var data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return data, nil
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func hashReaderN(t *testing.T, r io.Reader, n int64) []byte {
	t.Helper()
	h := sha256.New()
	if _, err := io.CopyN(h, r, n); err != nil {
		t.Fatalf("hash %d bytes: %v", n, err)
	}
	return h.Sum(nil)
}

func hashReader(t *testing.T, r io.Reader) []byte {
	t.Helper()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		t.Fatalf("hash reader: %v", err)
	}
	return h.Sum(nil)
}
