package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/state"
)

func TestExpose_RequiresServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	// No --server and no ROUTEUP_SERVER -> error before anything else.
	err := runExpose(cmd, nil, t.TempDir(), func(string) string { return "" }, exposeOpts{port: 8080})
	if err == nil || !strings.Contains(err.Error(), "no server") {
		t.Errorf("expected 'no server' error, got %v", err)
	}
}

func TestExpose_RejectedMultiLabel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// A multi-label public route is rejected before the agent is contacted.
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runExpose(cmd, []string{"foo.bar"}, t.TempDir(), func(string) string { return "" },
		exposeOpts{port: 8080, server: "https://example.invalid"})
	if err == nil || !strings.Contains(err.Error(), "single label") {
		t.Errorf("expected single-label rejection, got %v", err)
	}
}

func TestResolveServerToken_Precedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := state.WriteClientConfig(state.ClientConfig{Server: "https://cfg.example", Token: "cfg-token"}); err != nil {
		t.Fatal(err)
	}
	noEnv := func(string) string { return "" }
	withEnv := func(k string) string {
		switch k {
		case "ROUTEUP_SERVER":
			return "https://env.example"
		case "ROUTEUP_TOKEN":
			return "env-token"
		}
		return ""
	}

	// saved config is the lowest precedence
	if s, tok := resolveServerToken("", "", noEnv); s != "https://cfg.example" || tok != "cfg-token" {
		t.Errorf("config: got %q/%q", s, tok)
	}
	// env overrides config
	if s, tok := resolveServerToken("", "", withEnv); s != "https://env.example" || tok != "env-token" {
		t.Errorf("env: got %q/%q", s, tok)
	}
	// flag overrides env
	if s, tok := resolveServerToken("https://flag.example", "flag-token", withEnv); s != "https://flag.example" || tok != "flag-token" {
		t.Errorf("flag: got %q/%q", s, tok)
	}
}
