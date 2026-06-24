package server

import (
	"context"
	"crypto/tls"
	"sync"
	"testing"
)

// recordingCertManager records EnsureNamespace calls for assertions.
type recordingCertManager struct {
	mu      sync.Mutex
	ensured []string
}

func (m *recordingCertManager) TLSConfig() *tls.Config { return &tls.Config{} }

func (m *recordingCertManager) EnsureNamespace(_ context.Context, base string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensured = append(m.ensured, base)
}

func TestPrewarmNamespaceCerts(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	if _, _, err := store.CreateToken(ctx, "mukul", []AllowPattern{allowPattern(t, "*.mukul.routeup.dev")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateToken(ctx, "team", []AllowPattern{allowPattern(t, "*.team.routeup.dev")}); err != nil {
		t.Fatal(err)
	}
	// a root-tier token: its base IS the domain and must be skipped (managed at startup)
	if _, _, err := store.CreateToken(ctx, "root", []AllowPattern{allowPattern(t, "*.routeup.dev")}); err != nil {
		t.Fatal(err)
	}

	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x"}
	srv, err := NewWithStore(cfg, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingCertManager{}
	srv.cm = rec

	srv.prewarmNamespaceCerts(ctx)

	got := map[string]bool{}
	for _, b := range rec.ensured {
		got[b] = true
	}
	if !got["mukul.routeup.dev"] || !got["team.routeup.dev"] {
		t.Errorf("expected mukul + team namespaces ensured, got %v", rec.ensured)
	}
	if got["routeup.dev"] {
		t.Errorf("root domain must not be ensured (managed at startup), got %v", rec.ensured)
	}
}
