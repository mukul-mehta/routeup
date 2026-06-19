package state

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentSocketPath_EnvOverride(t *testing.T) {
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
