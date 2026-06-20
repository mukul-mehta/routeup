package cli

import (
	"bytes"
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

func TestLogs_StubMessage(t *testing.T) {
	stdout, stderr, err := runRoot(t, "logs")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	const want = "logs: not implemented yet"
	if !strings.Contains(stdout, want) {
		t.Errorf("logs output missing %q\n--- got ---\n%s", want, stdout)
	}
}
