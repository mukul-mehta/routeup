package state

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentSocketPath_EnvOverride(t *testing.T) {
	t.Setenv(StateDirEnv, t.TempDir())
	t.Setenv(AgentSocketEnv, "/tmp/test.sock")
	got, err := AgentSocketPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/test.sock" {
		t.Errorf("got %q, want /tmp/test.sock", got)
	}
}

func TestAgentSocketPath_XDGOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_RUNTIME_DIR is only honored on Linux")
	}
	t.Setenv(AgentSocketEnv, "")
	t.Setenv(StateDirEnv, "")
	t.Setenv(XDGRuntimeEnv, "/run/user/1000")

	got, err := AgentSocketPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/run/user/1000/routeup/agent.sock"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAgentSocketPath_HomeFallback(t *testing.T) {
	t.Setenv(AgentSocketEnv, "")
	t.Setenv(StateDirEnv, "")
	if runtime.GOOS == "linux" {
		t.Setenv(XDGRuntimeEnv, "")
	}

	got, err := AgentSocketPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".routeup", "agent.sock")) {
		t.Errorf("path %q should end with .routeup/agent.sock", got)
	}
}

func TestStateDirOverrideControlsAllStatePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "routeup-dev")
	t.Setenv(StateDirEnv, dir)
	t.Setenv(AgentSocketEnv, "")
	t.Setenv(XDGRuntimeEnv, filepath.Join(t.TempDir(), "xdg"))

	pathFuncs := map[string]struct {
		resolve func() (string, error)
		name    string
	}{
		"socket":         {AgentSocketPath, AgentSocketName},
		"log":            {AgentLogPath, AgentLogName},
		"pid":            {AgentPIDPath, AgentPIDName},
		"CA certificate": {CACertPath, CACertName},
		"CA key":         {CAKeyPath, CAKeyName},
		"client config":  {ClientConfigPath, ClientConfigName},
		"setup marker":   {SetupMarkerPath, SetupMarkerName},
	}
	for label, tt := range pathFuncs {
		got, err := tt.resolve()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if want := filepath.Join(dir, tt.name); got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
}

func TestStateDirOverrideResolvesRelativePath(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	t.Setenv(StateDirEnv, "state/dev")

	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(workingDir, "state", "dev"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestEmbeddedDevelopmentStateDir(t *testing.T) {
	previous := defaultDir
	defaultDir = filepath.Join(t.TempDir(), "embedded-state")
	t.Cleanup(func() { defaultDir = previous })
	t.Setenv(StateDirEnv, "")
	t.Setenv(AgentSocketEnv, "")
	t.Setenv(XDGRuntimeEnv, filepath.Join(t.TempDir(), "xdg"))

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != defaultDir || !IsDirOverridden() {
		t.Fatalf("Dir() = %q, overridden = %t", dir, IsDirOverridden())
	}
	socket, err := AgentSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(defaultDir, AgentSocketName); socket != want {
		t.Fatalf("AgentSocketPath() = %q, want %q", socket, want)
	}
}

func TestStateDirEnvOverridesEmbeddedDefault(t *testing.T) {
	previous := defaultDir
	defaultDir = filepath.Join(t.TempDir(), "embedded-state")
	t.Cleanup(func() { defaultDir = previous })
	want := filepath.Join(t.TempDir(), "environment-state")
	t.Setenv(StateDirEnv, want)

	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestEnsureParentDir(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "nested", "agent.sock")

	if err := EnsureParentDir(sock); err != nil {
		t.Fatalf("EnsureParentDir: %v", err)
	}
	if err := EnsureParentDir(sock); err != nil {
		t.Fatalf("EnsureParentDir (second call): %v", err)
	}
}
