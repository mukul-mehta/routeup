package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	dir, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(dir, "missing.sock"))
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

func TestDoctor_JSONReportsFailedChecks(t *testing.T) {
	isolateRouteupState(t)
	stdout, _, err := runRoot(t, "doctor", "--json")
	if err == nil {
		t.Fatal("expected failed doctor exit")
	}
	var output struct {
		Healthy bool `json:"healthy"`
		Checks  []struct {
			Level string `json:"level"`
		} `json:"checks"`
	}
	if decodeErr := json.Unmarshal([]byte(stdout), &output); decodeErr != nil {
		t.Fatalf("decode doctor json: %v\n%s", decodeErr, stdout)
	}
	if output.Healthy || len(output.Checks) == 0 {
		t.Fatalf("doctor output = %#v", output)
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
