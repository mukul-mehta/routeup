package server

// This file holds the public server's ingress tests. Unlike the tunnel package's
// transport-only tests, these run the REAL server stack — a SQLite store, real
// tokens, the Authorizer, the routeBroker, and the actual top-level handler
// (srv.handler() → serveIngress) — and push traffic through it by Host. They
// prove the same behaviors hold across the production public path, not just the
// transport in isolation.
//
// Harness (startIngressTunnel): builds a real store in a temp dir, mints a token
// scoped to *.alice.routeup.dev, starts an httptest server on srv.handler(), then
// connects a real tunnel.Client claiming route "hmr". One httptest server is
// enough here: handler() splits by URL path, so the same server handles both the
// agent's /_routeup/tunnel dial and the public ingress requests. OnGranted
// delivers the granted public host; cleanup cancels the client, closes the server
// and store, and waits for the client goroutine to exit. As in the tunnel tests
// this is a real listener so WebSocket upgrades can be hijacked.
//
// Public requests are aimed at the test server's URL but carry the granted public
// Host (req.Host / DialOptions.Host); that Host is how serveIngress finds the
// holding session.
//
// Tests:
//   - TestIngress_EndToEnd: a token-authorized claim, then a public GET routed by
//     Host reaches the agent backend with Host and path intact. Base case for the
//     full stack (auth + persistence + ingress).
//   - TestIngress_NoTunnel503: a Host that nothing holds returns 503.
//   - TestIngress_WebSocketHMR: a real WebSocket through serveIngress — same
//     assertions as the tunnel-level WS test, but proving the server's path-split
//     router and the real authorize/broker path don't break the upgrade.
//   - TestIngress_SSEStreamsIncrementally: gate-based proof that SSE flushes
//     incrementally through the real ingress (not buffered to EOF).
//   - TestIngress_ClientDisconnectCancelsUpstream: the key lifecycle test. The
//     backend blocks on r.Context().Done(); the test reads one flushed chunk,
//     cancels the public request, and asserts the backend observes cancellation.
//     This verifies the whole chain: public client disconnect → server request
//     ctx cancelled → ReverseProxy aborts the RoundTrip → yamux stream closes →
//     agent http.Server cancels its handler ctx → backend sees Done().
//
// Helpers: ingressWSURL rewrites the scheme for WebSocket dialing; ingressReadSSE
// Data (and the WithTimeout watchdog wrapper) parse one SSE event. TestIngress_
// EndToEnd and _NoTunnel503 predate M6; the streaming tests and startIngressTunnel
// were added for M6. (allowPattern and newServerWithStore are shared helpers
// defined elsewhere in the package's test files.)

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mukul-mehta/routeup/internal/proxy"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/streamtest"
	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// TestIngress_EndToEnd runs a real server (store + authorizer + hub), connects a
// tunnel client, and verifies a public request routed by Host reaches the
// agent's backend.
func TestIngress_EndToEnd(t *testing.T) {
	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "ingress.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, secret, err := store.CreateToken(context.Background(), "alice",
		[]AllowPattern{allowPattern(t, "*.alice.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}

	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x", PublicNamespace: "try"}
	srv := newServerWithStore(t, cfg, store)
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	// Agent's local backend.
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "backend saw host=%s path=%s", r.Host, r.URL.Path)
	})

	grantedCh := make(chan string, 1)
	client := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: ts.URL,
		Token:     secret,
		Spec:      tunnel.ClaimSpec{Route: "myapp"},
		Handler:   backend,
		OnGranted: func(h string) { grantedCh <- h },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	var host string
	select {
	case host = <-grantedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no claim grant")
	}
	if host != "myapp.alice.routeup.dev" {
		t.Fatalf("host = %q", host)
	}

	// Public request: connect to the test server but send the public Host.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/hello", nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("public request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "backend saw host=myapp.alice.routeup.dev") {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(string(body), "path=/hello") {
		t.Errorf("path not forwarded: %q", body)
	}
}

func TestIngress_NoTunnel503(t *testing.T) {
	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "i2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x"}
	srv := newServerWithStore(t, cfg, store)
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Host = "ghost.alice.routeup.dev"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestIngress_PathTargetsAndExposePaths(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "app")
	}))
	defer app.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "api")
	}))
	defer api.Close()

	targets := []route.Target{
		{Path: "/", Port: testServerPort(t, app.URL)},
		{Path: "/api", Port: testServerPort(t, api.URL)},
	}
	publicURL, host, cleanup := startIngressTunnel(t, proxy.NewTargets(targets, []string{"/api/*"}, nil))
	defer cleanup()

	assertIngressBody(t, publicURL+"/api/ping", host, http.StatusOK, "api")
	assertIngressBody(t, publicURL+"/", host, http.StatusNotFound, "routeup: path is not exposed\n")
}

func TestIngress_WebSocketHMR(t *testing.T) {
	publicURL, host, cleanup := startIngressTunnel(t, streamtest.WSHMR(streamtest.WSOptions{
		Subprotocols: []string{"vite-hmr"},
		Greeting:     "hmr:connected",
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, ingressWSURL(publicURL, "/hmr"), &websocket.DialOptions{
		Host:         host,
		Subprotocols: []string{"vite-hmr"},
	})
	if err != nil {
		t.Fatalf("dial websocket through ingress: %v", err)
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

func assertIngressBody(t *testing.T, url, host string, wantStatus int, wantBody string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus || string(body) != wantBody {
		t.Fatalf("status/body = %d %q, want %d %q", resp.StatusCode, body, wantStatus, wantBody)
	}
}

func testServerPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse test server url %q: %v", raw, err)
	}
	_, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split test server host %q: %v", u.Host, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse test server port %q: %v", portText, err)
	}
	return port
}

func TestIngress_SSEStreamsIncrementally(t *testing.T) {
	gate := make(chan struct{})
	publicURL, host, cleanup := startIngressTunnel(t, streamtest.SSEHMR([]string{"one", "two"}, gate))
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, publicURL+"/_next/webpack-hmr", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get sse through ingress: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	reader := bufio.NewReader(resp.Body)
	first := ingressReadSSEDataWithTimeout(t, reader, 2*time.Second)
	if first != "one" {
		t.Fatalf("first SSE event = %q, want one", first)
	}
	gate <- struct{}{}
	second := ingressReadSSEDataWithTimeout(t, reader, 2*time.Second)
	if second != "two" {
		t.Fatalf("second SSE event = %q, want two", second)
	}
}

func TestIngress_ClientDisconnectCancelsUpstream(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "chunk\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(cancelled)
	})
	publicURL, host, cleanup := startIngressTunnel(t, backend)
	defer cleanup()

	reqCtx, cancelReq := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, publicURL+"/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("start cancellable request: %v", err)
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	if line != "chunk\n" {
		t.Fatalf("first chunk = %q, want chunk", line)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not start handling request")
	}
	cancelReq()
	_ = resp.Body.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not observe request context cancellation")
	}
}

func startIngressTunnel(t *testing.T, backend http.Handler) (publicURL string, host string, cleanup func()) {
	t.Helper()

	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "ingress-streaming.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := store.CreateToken(context.Background(), "alice",
		[]AllowPattern{allowPattern(t, "*.alice.routeup.dev")})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}

	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x", PublicNamespace: "try"}
	srv := newServerWithStore(t, cfg, store)
	ts := httptest.NewServer(srv.handler())

	grantedCh := make(chan string, 1)
	client := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: ts.URL,
		Token:     secret,
		Spec:      tunnel.ClaimSpec{Route: "hmr"},
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
		ts.Close()
		_ = store.Close()
		t.Fatal("timed out waiting for claim grant")
	}

	cleanup = func() {
		cancel()
		ts.Close()
		_ = store.Close()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Log("tunnel client did not exit before cleanup timeout")
		}
	}
	return ts.URL, host, cleanup
}

func ingressWSURL(httpURL, path string) string {
	u := strings.TrimRight(httpURL, "/") + path
	return "ws://" + strings.TrimPrefix(u, "http://")
}

func ingressReadSSEDataWithTimeout(t *testing.T, r *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	ch := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := ingressReadSSEData(r)
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

func ingressReadSSEData(r *bufio.Reader) (string, error) {
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
