//go:build integration

// Real dev-server integration tests for the tunnel path. These spin up an
// actual Vite and Next.js dev server, expose each through the real public
// ingress (the same startIngressTunnel harness the synthetic tests use), and
// drive its HMR channel end-to-end:
//
//   - Vite: HMR is a WebSocket (subprotocol "vite-hmr") carrying {"type":...}
//     JSON. We assert the upgrade survives the tunnel, the server's "connected"
//     push arrives, and editing a watched file produces a real HMR push
//     (full-reload/update).
//   - Next: HMR is also a WebSocket (since Next 12) at /_next/webpack-hmr,
//     carrying {"action":...} JSON. We assert the upgrade survives the tunnel,
//     the initial "sync" arrives, and editing a page produces a build event
//     (building/built).
//
// They are gated behind the `integration` build tag so the normal suite
// (`go test ./...`) never runs them, and they Skip when node/npm are absent.
// They need network access for `npm install` and are intentionally heavy:
//
//	go test -tags integration -run TestIntegration -timeout 15m ./internal/server
//	# or: just test-integration
//
// Fidelity note: real routeup preserves the public Host to the local app, so
// these proxy with the public Host intact and configure Vite's allowedHosts to
// accept it — the same thing a real routeup user does. The transport-invariant
// properties (cancellation, non-buffering, large-body integrity) stay in the
// fast synthetic tests; these cover end-to-end fidelity only.
//
// Why this exists alongside the synthetic streamtest backends: the synthetic
// tests prove transport invariants a real dev server can't be instrumented to
// show deterministically; these prove the real thing actually works (a genuine
// Vite/Next HMR session survives the tunnel). Both layers are intentional.
//
// Per-test shape (both tests follow the same three steps):
//
//  1. Scaffold a minimal project in a temp dir, `npm install`, start the dev
//     server on a free loopback port, and wait until it answers HTTP.
//
//  2. Expose that port through the real public ingress via startIngressTunnel
//     (shared with the synthetic ingress tests), using devServerProxy as the
//     agent-side backend so the public Host is preserved to the dev server.
//
//  3. Drive the dev server's real HMR channel through the public URL and assert
//     a file edit produces a live HMR push.
//
// The two tests differ only in the dev server and its HMR channel:
//
//   - TestIntegration_ViteHMR: fetch index.html through the tunnel (marker
//     check), dial the vite-hmr WebSocket and read the "connected" push, then
//     edit index.html and wait for a full-reload/update/prune push.
//   - TestIntegration_NextHMR: fetch the rendered page, dial the
//     /_next/webpack-hmr WebSocket and read the initial "sync", then edit
//     pages/index.js and wait for a building/built push.
//
// Helpers:
//   - requireDevTooling: Skip the test if node/npm aren't on PATH.
//   - freePort: reserve an ephemeral loopback port for the dev server.
//   - writeFiles / viteIndexHTML / nextIndexJS: scaffold project files.
//   - npmInstall: `npm install` with a bounded context.
//   - startDevServer: launch the dev binary in its own process group and, on
//     cleanup, SIGKILL the whole group (npm/next spawn child processes); output
//     is captured in a safeBuffer for failure diagnostics.
//   - waitForHTTP: poll until the dev server returns 200 (Next compiles on first
//     request, so the caller passes a generous timeout).
//   - devServerProxy: the agent-side reverse proxy to the dev server; mirrors the
//     real agent tunnel handler's public Host preservation.
//   - getThroughTunnel: GET a public URL with the granted Host and return the body.
//   - readHMRField: read one HMR JSON message off the WebSocket and return the
//     value at a key — Vite tags messages "type", Next tags them "action", so one
//     reader serves both.
//   - safeBuffer: a mutex-guarded buffer so the child process's concurrent
//     stdout/stderr writes don't race the test reading them.
//
// Provenance: generated with the help of Claude as part of M6
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestIntegration_ViteHMR(t *testing.T) {
	requireDevTooling(t)

	dir := t.TempDir()
	port := freePort(t)
	writeFiles(t, dir, map[string]string{
		"package.json": `{
  "name": "routeup-vite-it",
  "private": true,
  "type": "module",
  "version": "0.0.0",
  "devDependencies": { "vite": "^6.0.0" }
}`,
		"vite.config.js": `import { defineConfig } from 'vite'
export default defineConfig({
  server: { host: '127.0.0.1', strictPort: true, allowedHosts: true },
})`,
		"index.html": viteIndexHTML(""),
		"main.js": `export const value = 'v0'
if (import.meta.hot) { import.meta.hot.accept() }`,
	})

	npmInstall(t, dir)
	logs := &safeBuffer{}
	startDevServer(t, dir, "node_modules/.bin/vite",
		[]string{"--port", strconv.Itoa(port), "--host", "127.0.0.1", "--strictPort"}, logs)
	waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", port), 60*time.Second, logs)

	publicURL, host, cleanup := startIngressTunnel(t, devServerProxy(port))
	defer cleanup()

	// 1) The app's HTML reaches us through the tunnel.
	if body := getThroughTunnel(t, publicURL+"/", host); !strings.Contains(body, "routeup-vite-integration-marker") {
		t.Fatalf("vite index through tunnel missing marker:\n%s", body)
	}

	// 2) The HMR WebSocket upgrades through the tunnel and Vite pushes "connected".
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelDial()
	conn, _, err := websocket.Dial(dialCtx, ingressWSURL(publicURL, "/"), &websocket.DialOptions{
		Host:         host,
		Subprotocols: []string{"vite-hmr"},
	})
	if err != nil {
		t.Fatalf("dial vite HMR websocket through tunnel: %v\n--- vite logs ---\n%s", err, logs.String())
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	if got := conn.Subprotocol(); got != "vite-hmr" {
		t.Fatalf("negotiated subprotocol = %q, want vite-hmr", got)
	}
	if mt := readHMRField(t, conn, "type", 20*time.Second); mt != "connected" {
		t.Fatalf("first vite HMR message type = %q, want connected", mt)
	}

	// 3) A real HMR push: editing index.html makes Vite send a full reload.
	writeFiles(t, dir, map[string]string{"index.html": viteIndexHTML("<!-- edited -->")})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("no Vite HMR push (full-reload/update) after editing index.html")
		}
		switch readHMRField(t, conn, "type", time.Until(deadline)) {
		case "full-reload", "update", "prune":
			return // got a real HMR push end-to-end
		}
	}
}

func TestIntegration_NextHMR(t *testing.T) {
	requireDevTooling(t)

	dir := t.TempDir()
	port := freePort(t)
	writeFiles(t, dir, map[string]string{
		// Pinned to a pages-router webpack release. Next's HMR is a WebSocket at
		// /_next/webpack-hmr carrying {"action":...} JSON (sync/building/built).
		"package.json": `{
  "name": "routeup-next-it",
  "private": true,
  "version": "0.0.0",
  "dependencies": { "next": "14.2.15", "react": "18.3.1", "react-dom": "18.3.1" }
}`,
		"next.config.js": `module.exports = { reactStrictMode: false }`,
		"pages/index.js": nextIndexJS("routeup-next-integration-marker"),
	})

	npmInstall(t, dir)
	logs := &safeBuffer{}
	startDevServer(t, dir, "node_modules/.bin/next",
		[]string{"dev", "-p", strconv.Itoa(port), "-H", "127.0.0.1"}, logs)
	// First request triggers a compile, so allow generous time.
	waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", port), 150*time.Second, logs)

	publicURL, host, cleanup := startIngressTunnel(t, devServerProxy(port))
	defer cleanup()

	// 1) The rendered page reaches us through the tunnel.
	if body := getThroughTunnel(t, publicURL+"/", host); !strings.Contains(body, "routeup-next-integration-marker") {
		t.Fatalf("next index through tunnel missing marker:\n%s", body)
	}

	// 2) The HMR WebSocket upgrades through the tunnel; Next pushes "sync" on connect.
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelDial()
	conn, _, err := websocket.Dial(dialCtx, ingressWSURL(publicURL, "/_next/webpack-hmr"), &websocket.DialOptions{
		Host: host,
	})
	if err != nil {
		t.Fatalf("dial next HMR websocket through tunnel: %v\n--- next logs ---\n%s", err, logs.String())
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	if action := readHMRField(t, conn, "action", 20*time.Second); action != "sync" {
		t.Fatalf("first next HMR action = %q, want sync", action)
	}

	// 3) A real HMR push: editing the page makes Next emit building/built.
	writeFiles(t, dir, map[string]string{"pages/index.js": nextIndexJS("routeup-next-integration-edited")})
	deadline := time.Now().Add(60 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("no Next HMR push (building/built) after editing pages/index.js")
		}
		switch readHMRField(t, conn, "action", time.Until(deadline)) {
		case "building", "built":
			return // got a real HMR push end-to-end
		}
	}
}

// --- helpers ---------------------------------------------------------------

func requireDevTooling(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"node", "npm"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("integration: %s not found in PATH", bin)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func npmInstall(t *testing.T, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "install", "--no-audit", "--no-fund", "--loglevel=error")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm install in %s: %v\n%s", dir, err, out)
	}
}

// startDevServer launches a local dev-server binary in its own process group so
// the cleanup can kill the whole tree (npm/next spawn children). Output is
// captured into logs for failure diagnostics.
func startDevServer(t *testing.T, dir, bin string, args []string, logs *safeBuffer) {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, bin), args...)
	cmd.Dir = dir
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // negative pid => process group
		}
		_ = cmd.Wait()
	})
}

func waitForHTTP(t *testing.T, url string, timeout time.Duration, logs *safeBuffer) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("dev server not ready at %s after %s: %v\n--- logs ---\n%s", url, timeout, lastErr, logs.String())
}

// devServerProxy mirrors the agent tunnel handler's reverse proxy to the local
// dev server, preserving the public Host header end-to-end.
func devServerProxy(port int) http.Handler {
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
		},
	}
}

func getThroughTunnel(t *testing.T, url, host string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s (Host %s): %v", url, host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %s", url, resp.StatusCode, body)
	}
	return string(body)
}

// readHMRField reads one HMR JSON message off the WebSocket and returns the
// string value at key. Vite tags messages with "type"; Next tags them with
// "action" — same transport, different field, so one reader covers both.
func readHMRField(t *testing.T, conn *websocket.Conn, key string, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read HMR message: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode HMR message %q: %v", data, err)
	}
	s, _ := msg[key].(string)
	return s
}

func viteIndexHTML(extra string) string {
	return `<!doctype html>
<html>
  <head><meta charset="utf-8" /><title>routeup-it</title>` + extra + `</head>
  <body>
    <div id="app">routeup-vite-integration-marker</div>
    <script type="module" src="/main.js"></script>
  </body>
</html>`
}

func nextIndexJS(marker string) string {
	return `export default function Home() {
  return <div id="app">` + marker + `</div>
}`
}

// safeBuffer is a concurrency-safe buffer for capturing child-process output.
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
