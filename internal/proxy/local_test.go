package proxy

// This file tests the LOCAL .localhost reverse-proxy path (proxy.New) — the
// zero-network path a browser uses at https://<route>.localhost, with no tunnel,
// no public server, and no TLS involved. It proves the WebSocket and SSE
// streaming behaviors the tunnel path has also hold on the local proxy, since
// real Vite/Next HMR run over this path during ordinary local development too.
//
// Test double: testPortLookup is a map implementing proxy.PortLookup, so the
// proxy resolves route "hmr" to the backend's port without the agent registry.
//
// Harness (startLocalProxy): starts the streaming backend on one httptest server,
// extracts its port, and starts a second httptest server running proxy.New with a
// lookup mapping "hmr" → that port. The client sends requests with Host
// "hmr.localhost"; proxy.New strips ".localhost", looks up the port, and reverse-
// proxies to the backend. Real listeners (not recorders) so WS upgrades hijack.
//
// Tests:
//   - TestLocalProxy_WebSocketHMR: a real WebSocket through proxy.New — upgrade,
//     subprotocol, server push, and client echo, proving the local path carries
//     WebSocket traffic (Vite HMR locally).
//   - TestLocalProxy_SSEStreamsIncrementally: gate-based proof that SSE flushes
//     incrementally through the local proxy (not buffered to EOF).
//
// Helpers: testServerPort parses the port out of an httptest URL; localWSURL
// rewrites http→ws for dialing; localReadSSEData (and the WithTimeout watchdog
// wrapper) parse one SSE event's data line.

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mukul-mehta/routeup/internal/streamtest"
)

type testPortLookup map[string]int

func (l testPortLookup) LookupPort(name string) (int, bool) {
	port, ok := l[name]
	return port, ok
}

func TestLocalProxy_WebSocketHMR(t *testing.T) {
	proxyURL, cleanup := startLocalProxy(t, streamtest.WSHMR(streamtest.WSOptions{
		Subprotocols: []string{"vite-hmr"},
		Greeting:     "hmr:connected",
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, localWSURL(proxyURL, "/hmr"), &websocket.DialOptions{
		Host:         "hmr.localhost",
		Subprotocols: []string{"vite-hmr"},
	})
	if err != nil {
		t.Fatalf("dial websocket through local proxy: %v", err)
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

func TestLocalProxy_SSEStreamsIncrementally(t *testing.T) {
	gate := make(chan struct{})
	proxyURL, cleanup := startLocalProxy(t, streamtest.SSEHMR([]string{"one", "two"}, gate))
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, proxyURL+"/_next/webpack-hmr", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "hmr.localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get sse through local proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	reader := bufio.NewReader(resp.Body)
	first := localReadSSEDataWithTimeout(t, reader, 2*time.Second)
	if first != "one" {
		t.Fatalf("first SSE event = %q, want one", first)
	}
	gate <- struct{}{}
	second := localReadSSEDataWithTimeout(t, reader, 2*time.Second)
	if second != "two" {
		t.Fatalf("second SSE event = %q, want two", second)
	}
}

func startLocalProxy(t *testing.T, backend http.Handler) (proxyURL string, cleanup func()) {
	t.Helper()
	backendServer := httptest.NewServer(backend)
	backendPort := testServerPort(t, backendServer.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxyServer := httptest.NewServer(New(testPortLookup{"hmr": backendPort}, logger))
	cleanup = func() {
		proxyServer.Close()
		backendServer.Close()
	}
	return proxyServer.URL, cleanup
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

func localWSURL(httpURL, path string) string {
	u := strings.TrimRight(httpURL, "/") + path
	return "ws://" + strings.TrimPrefix(u, "http://")
}

func localReadSSEDataWithTimeout(t *testing.T, r *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	ch := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := localReadSSEData(r)
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

func localReadSSEData(r *bufio.Reader) (string, error) {
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
