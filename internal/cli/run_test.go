package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/config"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd
}

func TestRun_NothingToRun(t *testing.T) {
	t.Setenv("ROUTEUP_NAME", "")

	err := runRun(newRunCmd(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "nothing to run") {
		t.Fatalf("expected 'nothing to run' error, got %v", err)
	}
}

func TestRun_CommandWithoutName(t *testing.T) {
	t.Setenv("ROUTEUP_NAME", "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "routeup.json"), []byte(`{"command":"true"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runRun(newRunCmd(), dir)
	if err == nil || !strings.Contains(err.Error(), "no route name") {
		t.Fatalf("expected 'no route name' error, got %v", err)
	}
}

func TestRunnerTargets_EnvPort(t *testing.T) {
	t.Setenv("ROUTEUP_PORT", "9090")
	targets, port, err := runnerTargets(config.Config{Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if port != 9090 || len(targets) != 1 || targets[0].Port != 9090 {
		t.Fatalf("targets = %#v, port = %d", targets, port)
	}
}
