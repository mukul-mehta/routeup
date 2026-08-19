package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

// TestUninstall_CancelsOnNo exercises the confirmation guard without
// touching the system: answering "n" must bail before any teardown.
func TestUninstall_CancelsOnNo(t *testing.T) {
	cmd := newUninstallCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected 'cancelled', got:\n%s", out.String())
	}
}

func TestUninstallAbortsWhenAgentDoesNotStop(t *testing.T) {
	t.Setenv(state.StateDirEnv, t.TempDir())
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(ipc.Status{BootID: "still-running"})
		case ipc.PathShutdown:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ignored"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv(state.AgentSocketEnv, socketPath)
	cmd := newRootCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err := stopOwnersAndAgent(cmd, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stop agent before uninstall") {
		t.Fatalf("error = %v", err)
	}
}

func TestUninstallRejectsActiveUncontrolledOwner(t *testing.T) {
	t.Setenv(state.StateDirEnv, t.TempDir())
	lease, err := state.RegisterOwner("runner", state.OwnerRunner, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(ipc.Status{UptimeSeconds: 10})
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathRoutes:
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": []ipc.Claim{{Name: "runner", OwnerPID: 42}}})
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathOwners+"/runner/stop":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(ipc.ErrorBody{Error: "route owner cannot be stopped remotely"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv(state.AgentSocketEnv, socketPath)
	cmd := newRootCmd()
	err = stopActiveOwners(cmd, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "runner owner for route \"runner\" is active") {
		t.Fatalf("error = %v", err)
	}
}

func TestUninstallRejectsStandaloneExposure(t *testing.T) {
	t.Setenv(state.StateDirEnv, t.TempDir())
	lease, err := state.RegisterOwner("public", state.OwnerExpose, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case ipc.PathRoutes:
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": []ipc.Claim{}})
		case ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(ipc.Status{UptimeSeconds: 10, Exposures: []ipc.ExposureStatus{{Route: "public", OwnerPID: 42}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv(state.AgentSocketEnv, socketPath)
	cmd := newRootCmd()
	err = stopActiveOwners(cmd, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "expose owner for route \"public\" is active") {
		t.Fatalf("error = %v", err)
	}
}

func TestUninstall_IsolatedStateSkipsGlobalTeardown(t *testing.T) {
	root, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(state.StateDirEnv, dir)
	t.Setenv(state.AgentSocketEnv, filepath.Join(root, "missing.sock"))
	if err := state.WriteSetupMarker(&state.SetupMarker{Version: state.CurrentSetupVersion, TLSPort: 47444}); err != nil {
		t.Fatal(err)
	}

	cmd := newUninstallCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, state.SetupMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("setup marker still exists: %v", err)
	}
	if strings.Contains(out.String(), "port helper") || strings.Contains(out.String(), "trust store") {
		t.Fatalf("isolated uninstall attempted global teardown:\n%s", out.String())
	}
}

func TestUninstall_RefusesUnsafeStateDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(state.StateDirEnv, home)
	t.Setenv(state.AgentSocketEnv, filepath.Join(home, "missing.sock"))
	if err := os.WriteFile(filepath.Join(home, "sentinel"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newUninstallCmd()
	cmd.SetArgs([]string{"--yes"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsafe isolated state directory") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "sentinel")); err != nil {
		t.Fatalf("unsafe uninstall removed home contents: %v", err)
	}
}
