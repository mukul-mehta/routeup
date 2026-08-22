package agentctl

import (
	"strings"
	"testing"
)

func TestWithoutEnvRemovesOnlyNamedVariable(t *testing.T) {
	env := withoutEnv([]string{
		"HOME=/tmp/home",
		"ROUTEUP_TOKEN=secret",
		"ROUTEUP_SERVER=https://edge.example.com",
	}, "ROUTEUP_TOKEN")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "ROUTEUP_TOKEN=") {
		t.Fatalf("environment retained ROUTEUP_TOKEN: %q", joined)
	}
	if !strings.Contains(joined, "ROUTEUP_SERVER=https://edge.example.com") {
		t.Fatalf("environment removed unrelated value: %q", joined)
	}
}
