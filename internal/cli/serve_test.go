package cli

import (
	"bytes"
	"os"
	"path/filepath"
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

// writeFile is a small helper for placing files inside a temp tree.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestServe_DryRun_Positional(t *testing.T) {
	stdout, _, err := runServeIn(t, t.TempDir(), "api.myapp", "--port", "8080", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		"route: api.myapp",
		"local: https://api.myapp.localhost",
		"public: https://api.myapp.routeup.dev",
		"target: http://localhost:8080",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\n--- got ---\n%s", want, stdout)
		}
	}
}

func TestServe_DryRun_FromConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routeup.json", `{"name":"myapp","port":7070}`)

	stdout, _, err := runServeIn(t, dir, "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		"route: myapp",
		"local: https://myapp.localhost",
		"target: http://localhost:7070",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\n--- got ---\n%s", want, stdout)
		}
	}
}

func TestServe_DryRun_BareNamePrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routeup.json", `{"name":"myapp"}`)

	stdout, _, err := runServeIn(t, dir, "api", "--port", "8080", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stdout, "route: api.myapp") {
		t.Errorf("stdout missing %q\n--- got ---\n%s", "route: api.myapp", stdout)
	}
}

func TestServe_NoPort_Errors(t *testing.T) {
	_, _, err := runServeIn(t, t.TempDir(), "api.myapp", "--dry-run")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no port") {
		t.Errorf("error %q does not contain %q", err.Error(), "no port")
	}
}

func TestServe_NoName_Errors(t *testing.T) {
	_, _, err := runServeIn(t, t.TempDir(), "--port", "8080", "--dry-run")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no route name") {
		t.Errorf("error %q does not contain %q", err.Error(), "no route name")
	}
}
