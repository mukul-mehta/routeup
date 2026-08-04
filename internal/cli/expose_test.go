package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/state"
)

func TestExpose_RequiresServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ROUTEUP_SERVER", "")
	t.Setenv("ROUTEUP_TOKEN", "")
	// EnsureLocalCA runs first as a precondition; give it a CA so we reach the
	// server check this test is about.
	writeLocalCA(t)

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runExpose(cmd, nil, t.TempDir(), exposeOpts{port: 8080})
	if err == nil || !strings.Contains(err.Error(), "no server") {
		t.Errorf("expected 'no server' error, got %v", err)
	}
}

// Public names are a single label, so a dotted name is normalized to hyphens
// (api.myapp -> api-myapp) rather than rejected.
func TestNormalizePublicName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"myapp", "myapp"},
		{"api.myapp", "api-myapp"},
		{"a.b.c", "a-b-c"},
	}
	for _, c := range cases {
		name, err := route.Parse(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := normalizePublicName(name); got != c.want {
			t.Errorf("normalizePublicName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrintRouteLocal_NonDefaultTLSPort(t *testing.T) {
	var out bytes.Buffer
	printRouteLocal(&out, "api.myapp", 47443)
	want := "route: api.myapp\nlocal: https://api.myapp.localhost:47443\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestResolveExposeRoute(t *testing.T) {
	t.Setenv("ROUTEUP_NAME", "")

	got, err := resolveExposeRoute("api", config.Config{Name: "myapp"}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "api.myapp" {
		t.Fatalf("route = %q, want %q", got, "api.myapp")
	}

	if _, err := resolveExposeRoute("api..myapp", config.Config{}, false, ""); err == nil || !strings.Contains(err.Error(), "invalid route name") {
		t.Fatalf("expected invalid route name, got %v", err)
	}

	// No name set anywhere: falls back to the working-directory basename.
	got, err = resolveExposeRoute("", config.Config{}, false, "/projects/myservice")
	if err != nil {
		t.Fatalf("expected dirname fallback, got error: %v", err)
	}
	if got.String() != "myservice" {
		t.Fatalf("route = %q, want %q", got, "myservice")
	}
}

// writeLocalCA creates a real local CA under the test's HOME so commands that
// call certs.EnsureLocalCA() as a precondition can proceed.
func writeLocalCA(t *testing.T) {
	t.Helper()
	certPath, err := state.CACertPath()
	if err != nil {
		t.Fatal(err)
	}
	keyPath, err := state.CAKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := certs.Create(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
}

func TestResolveServerToken_Precedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ROUTEUP_SERVER", "")
	t.Setenv("ROUTEUP_TOKEN", "")
	if err := state.WriteClientConfig(state.ClientConfig{Server: "https://cfg.example", Token: "cfg-token"}); err != nil {
		t.Fatal(err)
	}

	if s, tok := resolveServerToken("", ""); s != "https://cfg.example" || tok != "cfg-token" {
		t.Errorf("config: got %q/%q", s, tok)
	}

	t.Setenv("ROUTEUP_SERVER", "https://env.example")
	t.Setenv("ROUTEUP_TOKEN", "env-token")
	if s, tok := resolveServerToken("", ""); s != "https://env.example" || tok != "env-token" {
		t.Errorf("env: got %q/%q", s, tok)
	}

	if s, tok := resolveServerToken("https://flag.example", "flag-token"); s != "https://flag.example" || tok != "flag-token" {
		t.Errorf("flag: got %q/%q", s, tok)
	}
}
