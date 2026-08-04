package process

import (
	"os"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func TestInjectEnv_LocalOnly(t *testing.T) {
	base := []string{"PATH=/usr/bin", "FOO=bar"}
	got := envMap(InjectEnv(base, EnvInputs{
		Port:     5173,
		LocalURL: "https://myapp.localhost",
		WorkDir:  "/proj",
	}))

	if got["PORT"] != "5173" {
		t.Errorf("PORT = %q, want 5173", got["PORT"])
	}
	if got["HOST"] != "127.0.0.1" {
		t.Errorf("HOST = %q, want 127.0.0.1", got["HOST"])
	}
	if got["ROUTEUP_LOCAL_URL"] != "https://myapp.localhost" {
		t.Errorf("ROUTEUP_LOCAL_URL = %q", got["ROUTEUP_LOCAL_URL"])
	}
	if got["ROUTEUP_URL"] != "https://myapp.localhost" {
		t.Errorf("ROUTEUP_URL = %q, want it to mirror the local URL", got["ROUTEUP_URL"])
	}
	wantPrefix := "/proj/node_modules/.bin" + string(os.PathListSeparator)
	if !strings.HasPrefix(got["PATH"], wantPrefix) {
		t.Errorf("PATH = %q, want prefix %q", got["PATH"], wantPrefix)
	}
	if !strings.Contains(got["PATH"], "/usr/bin") {
		t.Errorf("PATH = %q, want it to retain /usr/bin", got["PATH"])
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO = %q, want it preserved", got["FOO"])
	}
}

func TestInjectEnv_PublicURLOverridesROUTEUP_URL(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	got := envMap(InjectEnv(base, EnvInputs{
		Port:      8080,
		LocalURL:  "https://myapp.localhost",
		PublicURL: "https://myapp.example.com",
	}))

	if got["ROUTEUP_LOCAL_URL"] != "https://myapp.localhost" {
		t.Errorf("ROUTEUP_LOCAL_URL = %q, want local URL", got["ROUTEUP_LOCAL_URL"])
	}
	if got["ROUTEUP_URL"] != "https://myapp.example.com" {
		t.Errorf("ROUTEUP_URL = %q, want public URL", got["ROUTEUP_URL"])
	}
}

func TestInjectEnv_CACertPath(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	got := envMap(InjectEnv(base, EnvInputs{
		Port:       3000,
		LocalURL:   "https://myapp.localhost",
		CACertPath: "/home/user/.routeup/ca.crt",
	}))

	if got["NODE_EXTRA_CA_CERTS"] != "/home/user/.routeup/ca.crt" {
		t.Errorf("NODE_EXTRA_CA_CERTS = %q, want /home/user/.routeup/ca.crt", got["NODE_EXTRA_CA_CERTS"])
	}
}

func TestInjectEnv_NoCACertPath(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	got := envMap(InjectEnv(base, EnvInputs{
		Port:     3000,
		LocalURL: "https://myapp.localhost",
	}))

	if _, ok := got["NODE_EXTRA_CA_CERTS"]; ok {
		t.Errorf("NODE_EXTRA_CA_CERTS should not be set when CACertPath is empty, got %q", got["NODE_EXTRA_CA_CERTS"])
	}
}
