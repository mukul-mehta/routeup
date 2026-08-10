package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShowsResolvedValuesWithoutToken(t *testing.T) {
	dir := t.TempDir()
	configJSON := `{"name":"myapp","port":8080,"command":"go run .","expose":{"enabled":true}}`
	if err := os.WriteFile(filepath.Join(dir, "routeup.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("ROUTEUP_SERVER", "https://edge.example")
	t.Setenv("ROUTEUP_TOKEN", "sk_routeup_secret")

	stdout, _, err := runRoot(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"source: routeup.json", "route: myapp", "localhost:8080", "exposure enabled: true", "token: configured"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("config output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "sk_routeup_secret") {
		t.Fatalf("config output leaked token:\n%s", stdout)
	}

	stdout, _, err = runRoot(t, "config", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var view configView
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatal(err)
	}
	if view.Route != "myapp" || len(view.Targets) != 1 || !view.TokenConfigured {
		t.Fatalf("config view = %#v", view)
	}
}
