package cli

import (
	"bytes"
	"strings"
	"testing"
)

func runDoctorCmd(t *testing.T) (string, error) {
	t.Helper()
	cmd := newDoctorCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	return buf.String(), err
}

func isolateRouteupState(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")
}

func TestDoctor_NoSetupFails(t *testing.T) {
	isolateRouteupState(t)

	out, err := runDoctorCmd(t)
	if err == nil {
		t.Fatalf("expected error with no setup, got nil; output: %s", out)
	}
	if !strings.Contains(out, "[fail]") {
		t.Errorf("output missing [fail] line: %s", out)
	}
	if !strings.Contains(out, "routeup setup") {
		t.Errorf("output missing 'routeup setup' hint: %s", out)
	}
}

func TestDoctor_AfterSetupSucceeds(t *testing.T) {
	isolateRouteupState(t)

	if _, err := runSetupCmd(t); err != nil {
		t.Fatalf("setup: %v", err)
	}

	out, err := runDoctorCmd(t)
	if err != nil {
		t.Fatalf("doctor: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "[ok]") {
		t.Errorf("output missing [ok] line: %s", out)
	}
	if strings.Contains(out, "[fail]") {
		t.Errorf("output unexpectedly contains [fail]: %s", out)
	}
}
