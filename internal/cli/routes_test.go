package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
)

func TestRoutes_NoAgent(t *testing.T) {
	// Point at a socket that does not exist and cannot be auto-spawned to:
	// the routes command is read-only and must not start an agent.
	sock := filepath.Join(t.TempDir(), "missing.sock")
	t.Setenv("ROUTEUP_AGENT_SOCKET", sock)

	cmd := newRoutesCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("routes returned error: %v", err)
	}
	if !strings.Contains(out.String(), "no active routes (agent not running)") {
		t.Errorf("missing no-agent message; got %q", out.String())
	}
}

func TestRoutesEscapesTerminalControls(t *testing.T) {
	claim := ipc.Claim{
		Name: "myapp", Targets: []route.Target{{Path: "/api\x1b[2J", Port: 8080}},
		OwnerPID: 42, OwnerCWD: "/tmp/project\x1b]0;changed\a",
	}
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(struct {
			Routes []ipc.Claim `json:"routes"`
		}{Routes: []ipc.Claim{claim}})
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)

	cmd := newRoutesCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\a") {
		t.Fatalf("routes output contains terminal controls: %q", out.String())
	}
	if !strings.Contains(out.String(), `\x1b`) {
		t.Fatalf("routes output did not preserve escaped context: %q", out.String())
	}
}

func TestRoutes_PropagatesAgentErrors(t *testing.T) {
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)

	cmd := newRoutesCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected agent error")
	}
	if strings.Contains(out.String(), "agent not running") {
		t.Fatalf("agent error misreported as unavailable: %q", out.String())
	}
}
