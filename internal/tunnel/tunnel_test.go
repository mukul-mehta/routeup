package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBroker grants a host per route and tracks holds/releases.
type fakeBroker struct {
	mu       sync.Mutex
	held     map[string]bool
	released map[string]bool
	failTok  string // if token == failTok, Hold rejects
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
	if token == k.failTok {
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
		Token:     "good",
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
	broker.failTok = "bad"
	reg := NewTunnelRegistry(broker, nil)
	ts := httptest.NewServer(reg.AcceptHandler())
	defer ts.Close()

	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "bad",
		Spec:      ClaimSpec{Route: "api"},
		Handler:   http.NewServeMux(),
	})
	err := client.connectAndServe(context.Background())
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("err = %v, want PermanentError", err)
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("err = %v, want it to mention the reason", err)
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
		Token:     "good",
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
