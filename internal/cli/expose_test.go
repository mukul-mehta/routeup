package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/certs"
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
		{"", ""},
		{"myapp", "myapp"},
		{"api.myapp", "api-myapp"},
		{"a.b.c", "a-b-c"},
	}
	for _, c := range cases {
		if got := normalizePublicName(c.in); got != c.want {
			t.Errorf("normalizePublicName(%q) = %q, want %q", c.in, got, c.want)
		}
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
