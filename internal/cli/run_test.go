package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/route"
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

func TestWriteRunnerPreamble(t *testing.T) {
	var out bytes.Buffer
	name, err := route.Parse("integration-runner")
	if err != nil {
		t.Fatal(err)
	}
	writeRunnerPreamble(&out, "nest start --watch", name,
		"https://integration-runner.localhost:47444", "",
		[]route.Target{{Path: "/", Port: 61809}}, 61809)

	wantOrder := []string{
		"routeup",
		"command nest start --watch",
		"route   integration-runner",
		"local   https://integration-runner.localhost:47444",
		"target  / -> localhost:61809",
		"status  waiting for localhost:61809",
	}
	remaining := out.String()
	for _, want := range wantOrder {
		index := strings.Index(remaining, want)
		if index < 0 {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
		remaining = remaining[index+len(want):]
	}
}
