package examples_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/config"
)

func TestExampleRouteupConfigs(t *testing.T) {
	cases := []struct {
		path        string
		name        string
		port        int
		targetCount int
		exposePaths []string
	}{
		{
			path:        "go-split/routeup.json",
			name:        "go-split",
			targetCount: 2,
			exposePaths: []string{"/api/*"},
		},
		{
			path: "node-basic/routeup.json",
			name: "node-basic",
			port: 5174,
		},
		{
			path:        "python-api/routeup.json",
			name:        "python-api",
			port:        8082,
			exposePaths: []string{"/api/webhooks/*"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			cfg, err := config.LoadRouteupJSON(filepath.FromSlash(tc.path))
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if cfg.Name != tc.name {
				t.Fatalf("name = %q, want %q", cfg.Name, tc.name)
			}
			if cfg.Port != tc.port {
				t.Fatalf("port = %d, want %d", cfg.Port, tc.port)
			}
			if len(cfg.Targets) != tc.targetCount {
				t.Fatalf("target count = %d, want %d", len(cfg.Targets), tc.targetCount)
			}
			if got, want := len(cfg.Expose.Paths), len(tc.exposePaths); got != want {
				t.Fatalf("expose path count = %d, want %d", got, want)
			}
			for i, want := range tc.exposePaths {
				if cfg.Expose.Paths[i] != want {
					t.Fatalf("expose path %d = %q, want %q", i, cfg.Expose.Paths[i], want)
				}
			}
		})
	}
}

func TestNodeExampleSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	runExampleCheck(t, node, "--check", filepath.FromSlash("node-basic/server.mjs"))
}

func TestPythonExampleSyntax(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	runExampleCheck(t, python, "-m", "py_compile", filepath.FromSlash("python-api/app.py"))
}

func runExampleCheck(t *testing.T, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}
