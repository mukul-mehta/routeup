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

// fakeKeeper grants a host per route and tracks holds/releases.
type fakeKeeper struct {
	mu       sync.Mutex
	held     map[string]bool
	released map[string]bool
	failTok  string // if token == failTok, Hold rejects
}

func newFakeKeeper() *fakeKeeper {
	return &fakeKeeper{held: map[string]bool{}, released: map[string]bool{}}
}

type codedErr struct {
	msg  string
	code int
}

func (e *codedErr) Error() string   { return e.msg }
func (e *codedErr) StatusCode() int { return e.code }

func (k *fakeKeeper) Hold(_ context.Context, token string, spec ClaimSpec) (string, error) {
	if token == k.failTok {
		return "", &codedErr{msg: "invalid token", code: http.StatusUnauthorized}
	}
	host := spec.Route + ".alice.routeup.dev"
	k.mu.Lock()
	k.held[host] = true
	k.mu.Unlock()
	return host, nil
}

func (k *fakeKeeper) Release(host string) {
	k.mu.Lock()
	k.released[host] = true
	k.mu.Unlock()
}

func (k *fakeKeeper) wasReleased(host string) bool {
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

	hub := NewHub(newFakeKeeper(), nil)
	ts := httptest.NewServer(hub.AcceptHandler())
	defer ts.Close()

	grantedCh := make(chan []string, 1)
	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "good",
		Specs:     []ClaimSpec{{Route: "api.myapp"}},
		Handler:   httputil.NewSingleHostReverseProxy(target),
		OnGranted: func(h []string) { grantedCh <- h },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	var hosts []string
	select {
	case hosts = <-grantedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for claim grant")
	}
	if len(hosts) != 1 || hosts[0] != "api.myapp.alice.routeup.dev" {
		t.Fatalf("granted = %v", hosts)
	}
	host := hosts[0]

	req, _ := http.NewRequest(http.MethodGet, "http://"+host+"/ping", nil)
	req.Host = host
	resp, err := hub.RoundTrip(host, req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
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
	hub := NewHub(newFakeKeeper(), nil)
	req, _ := http.NewRequest(http.MethodGet, "http://nobody.routeup.dev/", nil)
	if _, err := hub.RoundTrip("nobody.routeup.dev", req); !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}

func TestTunnel_ClaimRejected(t *testing.T) {
	keeper := newFakeKeeper()
	keeper.failTok = "bad"
	hub := NewHub(keeper, nil)
	ts := httptest.NewServer(hub.AcceptHandler())
	defer ts.Close()

	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "bad",
		Specs:     []ClaimSpec{{Route: "api"}},
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
	keeper := newFakeKeeper()
	hub := NewHub(keeper, nil)
	ts := httptest.NewServer(hub.AcceptHandler())
	defer ts.Close()

	grantedCh := make(chan []string, 1)
	client := NewClient(ClientOptions{
		ServerURL: ts.URL,
		Token:     "good",
		Specs:     []ClaimSpec{{Route: "api.myapp"}},
		Handler:   http.NewServeMux(),
		OnGranted: func(h []string) { grantedCh <- h },
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = client.Run(ctx) }()

	var host string
	select {
	case hosts := <-grantedCh:
		host = hosts[0]
	case <-time.After(5 * time.Second):
		t.Fatal("no grant")
	}

	cancel() // disconnect the agent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if keeper.wasReleased(host) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("claim for %s was not released after disconnect", host)
}
