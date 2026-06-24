package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
