package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
