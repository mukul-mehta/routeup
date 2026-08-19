package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

func TestStopRouteResolvesBareNameAndWaitsForRemoval(t *testing.T) {
	cwd := t.TempDir()
	writeConfig(t, cwd, `{"name":"myapp","port":8080}`)
	t.Chdir(cwd)
	var active atomic.Bool
	active.Store(true)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathOwners+"/api/stop":
			active.Store(false)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathRoutes:
			routes := []ipc.Claim{}
			if active.Load() {
				routes = append(routes, ipc.Claim{Name: "api", OwnerPID: 42})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": routes})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	stdout, _, err := runRoot(t, "stop", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "route stopped:") || !strings.Contains(stdout, "api") {
		t.Fatalf("output = %q", stdout)
	}
}

func TestStopRouteFailsClosedForUncontrolledOwner(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == ipc.PathOwners+"/myapp/stop" {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(ipc.ErrorBody{Error: "route owner cannot be stopped remotely", OwnerPID: 42})
			return
		}
		http.NotFound(w, r)
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	_, _, err := runRoot(t, "stop", "myapp")
	if err == nil || !strings.Contains(err.Error(), "cannot be stopped remotely") {
		t.Fatalf("error = %v", err)
	}
}

func TestStopRouteWaitsForOwnerToReconcileAfterAgentRestart(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv(state.StateDirEnv, t.TempDir())
	lease, err := state.RegisterOwner("myapp", state.OwnerServe, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	var stopCalls atomic.Int32
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathOwners+"/myapp/stop":
			if stopCalls.Add(1) < 3 {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(ipc.Status{UptimeSeconds: 0})
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathRoutes:
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": []ipc.Claim{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	stdout, _, err := runRoot(t, "stop", "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if stopCalls.Load() < 3 || !strings.Contains(stdout, "route stopped:") {
		t.Fatalf("stop calls = %d, output = %q", stopCalls.Load(), stdout)
	}
}

func TestStopRouteWaitsForOwnerControlAfterAgentRestart(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv(state.StateDirEnv, t.TempDir())
	lease, err := state.RegisterOwner("myapp", state.OwnerServe, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()

	var stopCalls atomic.Int32
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == ipc.PathOwners+"/myapp/stop":
			if stopCalls.Add(1) < 3 {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(ipc.ErrorBody{Error: "route owner cannot be stopped remotely; stop the holding process from its terminal"})
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == ipc.PathRoutes:
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": []ipc.Claim{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	stdout, _, err := runRoot(t, "stop", "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if stopCalls.Load() < 3 || !strings.Contains(stdout, "route stopped:") {
		t.Fatalf("stop calls = %d, output = %q", stopCalls.Load(), stdout)
	}
}

func TestServeDetachFlagUsesConventionalPair(t *testing.T) {
	flag := newServeCmd().Flags().Lookup("detach")
	if flag == nil || flag.Shorthand != "d" {
		t.Fatalf("detach flag = %#v, want -d/--detach", flag)
	}
	if newServeCmd().Flags().Lookup("background") != nil || newServeCmd().Flags().Lookup("bg") != nil {
		t.Fatal("serve exposes redundant background aliases")
	}
}

func TestDetachedServeRemovesTokenFromChildEnvironment(t *testing.T) {
	env := withoutEnv([]string{"HOME=/tmp/home", "ROUTEUP_TOKEN=secret", "ROUTEUP_SERVER=https://example.com"}, "ROUTEUP_TOKEN")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "ROUTEUP_TOKEN=") {
		t.Fatalf("sanitized environment still contains token: %q", joined)
	}
	if !strings.Contains(joined, "HOME=/tmp/home") || !strings.Contains(joined, "ROUTEUP_SERVER=https://example.com") {
		t.Fatalf("sanitized environment dropped unrelated values: %q", joined)
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "routeup.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
