package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer builds a Server wired to a fresh store + one token, plus its
// handler, without binding a port.
func newTestServer(t *testing.T, namespace string) (*Server, string) {
	t.Helper()
	store := openTestStore(t)
	_, secret, err := store.CreateToken(context.Background(), "alice",
		[]AllowPattern{allowPattern(t, "*.alice.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x", PublicNamespace: namespace}
	srv, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.store = store
	srv.authorizer = NewAuthorizer(cfg, store)
	return srv, secret
}

// newServerWithStore wires a store-backed Server for handler-level tests
// (New + attach), without binding a listener or cert manager. It is the
// test-only counterpart to Run's setup, keeping that seam out of server.go.
func newServerWithStore(t *testing.T, cfg ServerConfig, store *Store) *Server {
	t.Helper()
	srv, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.attach(store)
	return srv
}

func TestServerAPI_Health(t *testing.T) {
	srv, _ := newTestServer(t, "try")
	req := httptest.NewRequest(http.MethodGet, PathHealth, nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}
	var h HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" || h.Domain != "routeup.dev" || h.PublicNamespace != "try" {
		t.Errorf("health = %+v", h)
	}
}

func TestNew_InvalidConfig(t *testing.T) {
	if _, err := New(ServerConfig{Listen: ":8080", DBPath: "x"}, nil); err == nil {
		t.Errorf("expected error for missing domain")
	}
}
