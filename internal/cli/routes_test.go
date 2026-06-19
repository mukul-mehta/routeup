package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
