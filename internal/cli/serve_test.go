package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// runServeIn builds a fresh serve command, chdirs to dir (if non-empty),
// captures stdout+stderr, runs it with args (positional + flags), and returns
// the buffers along with any error.
//
// t.Chdir auto-restores the previous working directory at the end of the test.
func runServeIn(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	writeLocalCA(t)
	if dir != "" {
		t.Chdir(dir)
	}

	cmd := newServeCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestServe_NoTargets_Errors(t *testing.T) {
	_, _, err := runServeIn(t, t.TempDir(), "api.myapp")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no targets") {
		t.Errorf("error %q does not contain %q", err.Error(), "no targets")
	}
}

func TestServeUsesExposureConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLocalCA(t)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "routeup.json"), []byte(`{"name":"myapp","port":8080,"expose":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var exposeCalls atomic.Int32
	ownerReady := make(chan struct{}, 1)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(ipc.Status{BootID: "boot"})
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathRoutes:
			var claim ipc.Claim
			_ = json.NewDecoder(r.Body).Decode(&claim)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(claim)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, ipc.PathRoutes+"/"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathExpose:
			exposeCalls.Add(1)
			_ = json.NewEncoder(w).Encode(ipc.ExposeResponse{Host: "myapp.example"})
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathOwners+"/myapp":
			writeOwnerReady(w, r, ownerReady)
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathLogs:
			writeEmptyLogStream(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	t.Setenv("ROUTEUP_SERVER", "http://127.0.0.1:8080")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := newRootCmd()
	cmd.SetContext(ctx)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serve"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case <-ownerReady:
		cancel()
	case <-t.Context().Done():
		cancel()
		t.Fatal("serve did not enter desired-state maintenance")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if exposeCalls.Load() == 0 {
		t.Fatal("serve did not honor expose.enabled")
	}
}

func TestServeJSONWritesOnlyReadyEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLocalCA(t)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "routeup.json"), []byte(`{"name":"myapp","port":8080}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	outputWritten := make(chan struct{}, 1)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(ipc.Status{BootID: "boot"})
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathRoutes:
			var claim ipc.Claim
			_ = json.NewDecoder(r.Body).Decode(&claim)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(claim)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, ipc.PathRoutes+"/"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathOwners+"/myapp":
			writeOwnerReady(w, r, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := newRootCmd()
	cmd.SetContext(ctx)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(signalWriter{Writer: &stdout, written: outputWritten})
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"serve", "--json"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case <-outputWritten:
		cancel()
	case <-t.Context().Done():
		cancel()
		t.Fatal("serve did not enter maintenance")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var event routeReadyEvent
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout.String())
	}
	if event.Event != "ready" || event.Route != "myapp" || event.LocalURL != "https://myapp.localhost" {
		t.Fatalf("event = %#v", event)
	}
	if strings.Contains(stdout.String(), "press Ctrl-C") {
		t.Fatalf("JSON output contains human text: %s", stdout.String())
	}
}

func writeOwnerReady(w http.ResponseWriter, r *http.Request, ready chan<- struct{}) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	w.(http.Flusher).Flush()
	if ready != nil {
		select {
		case ready <- struct{}{}:
		default:
		}
	}
	<-r.Context().Done()
}

type signalWriter struct {
	io.Writer
	written chan<- struct{}
}

func (w signalWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	select {
	case w.written <- struct{}{}:
	default:
	}
	return n, err
}

func writeEmptyLogStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}
