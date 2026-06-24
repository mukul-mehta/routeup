package state

import (
	"os"
	"testing"
)

func TestClientConfig_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// missing file -> zero config, no error
	got, err := ReadClientConfig()
	if err != nil {
		t.Fatalf("ReadClientConfig(missing): %v", err)
	}
	if got.Server != "" || got.Token != "" {
		t.Errorf("missing config = %+v, want zero", got)
	}

	want := ClientConfig{Server: "https://edge.routeup.dev", Token: "sk_routeup_secret"}
	if err := WriteClientConfig(want); err != nil {
		t.Fatalf("WriteClientConfig: %v", err)
	}

	got, err = ReadClientConfig()
	if err != nil {
		t.Fatalf("ReadClientConfig: %v", err)
	}
	if got != want {
		t.Errorf("read = %+v, want %+v", got, want)
	}

	// the file must be 0600 (it holds a token)
	path, _ := ClientConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("client config perms = %o, want 600", perm)
	}
}

func TestClientConfigPath(t *testing.T) {
	t.Setenv("HOME", "/home/example")
	path, err := ClientConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/home/example/.routeup/client.json" {
		t.Errorf("path = %q", path)
	}
}
