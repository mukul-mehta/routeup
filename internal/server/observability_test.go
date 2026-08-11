package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type responseWriterSpy struct {
	header   http.Header
	statuses []int
	body     bytes.Buffer
}

func (w *responseWriterSpy) Header() http.Header { return w.header }

func (w *responseWriterSpy) WriteHeader(statusCode int) {
	w.statuses = append(w.statuses, statusCode)
}

func (w *responseWriterSpy) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func TestObservedResponseWriterPreservesInformationalResponses(t *testing.T) {
	underlying := &responseWriterSpy{header: make(http.Header)}
	observed := &observedResponseWriter{ResponseWriter: underlying}
	observed.WriteHeader(http.StatusEarlyHints)
	observed.WriteHeader(http.StatusCreated)
	_, _ = observed.Write([]byte("created"))

	if len(underlying.statuses) != 2 || underlying.statuses[0] != http.StatusEarlyHints || underlying.statuses[1] != http.StatusCreated {
		t.Fatalf("statuses = %v, want [103 201]", underlying.statuses)
	}
	if observed.status() != http.StatusCreated || observed.bytes != int64(len("created")) {
		t.Fatalf("observed status/bytes = %d/%d", observed.status(), observed.bytes)
	}
}

func TestIngressLoggingUsesSafeStructuredFields(t *testing.T) {
	store := openTestStore(t)
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x"}
	srv, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv.attach(store)

	req := httptest.NewRequest(http.MethodPost, "https://missing.routeup.dev/private?token=query-secret", strings.NewReader("body-secret"))
	req.Host = "MISSING.ROUTEUP.DEV:443"
	req.Header.Set("Authorization", "Bearer authorization-secret")
	req.Header.Set("Cookie", "session=cookie-secret")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log: %v\n%s", err, output.String())
	}
	if entry["msg"] != "public request completed" || entry["method"] != http.MethodPost {
		t.Fatalf("log entry = %#v", entry)
	}
	if entry["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("log entry = %#v", entry)
	}
	if _, ok := entry["host"]; ok {
		t.Fatalf("info log contains route host: %#v", entry)
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Fatalf("log missing duration_ms: %#v", entry)
	}
	if bytes, ok := entry["response_bytes"].(float64); !ok || bytes <= 0 {
		t.Fatalf("response_bytes = %#v", entry["response_bytes"])
	}
	for _, secret := range []string{"query-secret", "authorization-secret", "cookie-secret", "body-secret", "/private"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("log contains sensitive request data %q: %s", secret, output.String())
		}
	}
	if srv.metrics.requestsByClass[requestNoTunnel][4].Load() != 1 || srv.metrics.requestsInFlight.Load() != 0 {
		t.Fatalf("request metrics not recorded")
	}
}

func TestIngressHostIsOnlyLoggedAtDebug(t *testing.T) {
	store := openTestStore(t)
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x"}
	srv, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv.attach(store)

	req := httptest.NewRequest(http.MethodGet, "https://debug.routeup.dev/", nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2:\n%s", len(lines), output.String())
	}
	var infoEntry, debugEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &infoEntry); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &debugEntry); err != nil {
		t.Fatal(err)
	}
	if _, ok := infoEntry["host"]; ok {
		t.Fatalf("info log contains host: %#v", infoEntry)
	}
	if debugEntry["level"] != slog.LevelDebug.String() || debugEntry["host"] != "debug.routeup.dev" {
		t.Fatalf("debug log = %#v", debugEntry)
	}
}

func TestHealthCheckIsNotRequestLogged(t *testing.T) {
	store := openTestStore(t)
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":0", DBPath: "x"}
	srv, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv.attach(store)

	req := httptest.NewRequest(http.MethodGet, PathHealth, nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if output.Len() != 0 {
		t.Fatalf("health check was logged: %s", output.String())
	}
}
