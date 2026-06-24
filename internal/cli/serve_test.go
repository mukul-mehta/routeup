package cli

import (
	"bytes"
	"strings"
	"testing"
)

// runServeIn builds a fresh serve command, chdirs to dir (if non-empty),
// captures stdout+stderr, runs it with args (positional + flags), and returns
// the buffers along with any error.
//
// t.Chdir auto-restores the previous working directory at the end of the test.
func runServeIn(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
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

func TestServe_NoPort_Errors(t *testing.T) {
	_, _, err := runServeIn(t, t.TempDir(), "api.myapp")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no port") {
		t.Errorf("error %q does not contain %q", err.Error(), "no port")
	}
}

func TestServe_NoName_Errors(t *testing.T) {
	_, _, err := runServeIn(t, t.TempDir(), "--port", "8080")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no route name") {
		t.Errorf("error %q does not contain %q", err.Error(), "no route name")
	}
}
