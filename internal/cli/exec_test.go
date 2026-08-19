package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

func TestExecExplicitCommandInjectsProjectEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(state.StateDirEnv, "")
	writeLocalCA(t)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "routeup.json"), []byte(`{
  "name": "myapp",
  "targets": [{"path": "/", "port": 3000}]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var unexpected atomic.Int32
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != ipc.PathStatus {
			unexpected.Add(1)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(ipc.Status{Exposures: []ipc.ExposureStatus{{
			Route: "myapp", Host: "myapp.example.com", State: ipc.ExposureConnected,
		}}})
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)

	stdout, _, err := runRoot(t, "exec", "--", "env")
	if err != nil {
		t.Fatal(err)
	}
	env := parseEnvOutput(stdout)
	certPath, err := state.CACertPath()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOST":                "127.0.0.1",
		"PORT":                "3000",
		"ROUTEUP_LOCAL_URL":   "https://myapp.localhost",
		"ROUTEUP_URL":         "https://myapp.example.com",
		"NODE_EXTRA_CA_CERTS": certPath,
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("%s = %q, want %q", key, env[key], value)
		}
	}
	wantPathPrefix := filepath.Join(cwd, "node_modules", ".bin") + string(os.PathListSeparator)
	if !strings.HasPrefix(env["PATH"], wantPathPrefix) {
		t.Errorf("PATH = %q, want prefix %q", env["PATH"], wantPathPrefix)
	}
	if unexpected.Load() != 0 {
		t.Errorf("exec made %d unexpected agent requests", unexpected.Load())
	}
}

func TestExecUsesConfiguredCommandOrScript(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
	}{
		{
			name:     "routeup command",
			filename: "routeup.json",
			content:  `{"name":"myapp","command":"env"}`,
		},
		{
			name:     "package script",
			filename: "package.json",
			content:  `{"scripts":{"show-env":"env"},"routeup":{"name":"myapp","script":"show-env"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(state.StateDirEnv, "")
			writeLocalCA(t)
			cwd := t.TempDir()
			if err := os.WriteFile(filepath.Join(cwd, tt.filename), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(cwd)
			t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))

			stdout, _, err := runRoot(t, "exec")
			if err != nil {
				t.Fatal(err)
			}
			env := parseEnvOutput(stdout)
			if env["ROUTEUP_LOCAL_URL"] != "https://myapp.localhost" {
				t.Fatalf("ROUTEUP_LOCAL_URL = %q", env["ROUTEUP_LOCAL_URL"])
			}
			if env["ROUTEUP_URL"] != env["ROUTEUP_LOCAL_URL"] {
				t.Fatalf("ROUTEUP_URL = %q, want local fallback", env["ROUTEUP_URL"])
			}
		})
	}
}

func TestExecExplicitCommandOverridesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(state.StateDirEnv, "")
	writeLocalCA(t)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "routeup.json"), []byte(`{"name":"myapp","command":"exit 17"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))

	if _, _, err := runRoot(t, "exec", "--", "true"); err != nil {
		t.Fatalf("explicit command did not override config: %v", err)
	}
}

func TestExecArgumentValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "explicit command needs separator", args: []string{"exec", "env"}, want: "after --"},
		{name: "separator needs command", args: []string{"exec", "--"}, want: "command is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runRoot(t, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestExecRequiresConfiguredOrExplicitCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, err := runRoot(t, "exec")
	if err == nil || !strings.Contains(err.Error(), "nothing to execute") {
		t.Fatalf("error = %v, want missing command", err)
	}
}

func TestExecPropagatesExitCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(state.StateDirEnv, "")
	writeLocalCA(t)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "routeup.json"), []byte(`{"name":"myapp"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))

	_, _, err := runRoot(t, "exec", "--", "sh", "-c", "exit 7")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("error = %v, want exit code 7", err)
	}
}

func parseEnvOutput(output string) map[string]string {
	env := make(map[string]string)
	for line := range strings.SplitSeq(output, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	return env
}
