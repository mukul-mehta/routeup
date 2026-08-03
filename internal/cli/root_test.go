package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)

	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestRoot_Version(t *testing.T) {
	stdout, _, err := runRoot(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}

	const want = "0.0.0-dev"
	if !strings.Contains(stdout, want) {
		t.Errorf("--version output missing %q\n--- got ---\n%s", want, stdout)
	}
}

func TestLogs_NoAgentMessage(t *testing.T) {
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	stdout, stderr, err := runRoot(t, "logs")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	const want = "no request logs (agent not running)"
	if !strings.Contains(stdout, want) {
		t.Errorf("logs output missing %q\n--- got ---\n%s", want, stdout)
	}
}

func TestLogsRejectsConflictingSourceFlags(t *testing.T) {
	_, _, err := runRoot(t, "logs", "--public", "--local")
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("logs conflict error = %v", err)
	}
}
