package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultServerConfig(t *testing.T) {
	d := DefaultServerConfig()
	if d.Listen != ":443" {
		t.Errorf("default Listen = %q, want %q", d.Listen, ":443")
	}
	if d.DBPath == "" {
		t.Errorf("default DBPath should not be empty")
	}
	if d.TLSMode != TLSModeACME {
		t.Errorf("default TLSMode = %q, want %q", d.TLSMode, TLSModeACME)
	}
	if d.Domain != "" {
		t.Errorf("default Domain = %q, want empty", d.Domain)
	}
}

func TestOverlay(t *testing.T) {
	base := DefaultServerConfig()

	got := Overlay(base, ServerConfig{Domain: "routeup.dev"})
	if got.Domain != "routeup.dev" {
		t.Errorf("Domain = %q, want %q", got.Domain, "routeup.dev")
	}
	if got.Listen != ":443" {
		t.Errorf("Listen = %q, want default %q preserved", got.Listen, ":443")
	}

	// later layer wins; Reserved replaces only when non-nil
	withReserved := Overlay(got, ServerConfig{Listen: ":443", Reserved: []string{"billing"}})
	if withReserved.Listen != ":443" {
		t.Errorf("Listen = %q, want %q", withReserved.Listen, ":443")
	}
	if len(withReserved.Reserved) != 1 || withReserved.Reserved[0] != "billing" {
		t.Errorf("Reserved = %v, want [billing]", withReserved.Reserved)
	}
	if withReserved.Domain != "routeup.dev" {
		t.Errorf("Domain = %q, want preserved %q", withReserved.Domain, "routeup.dev")
	}
}

func TestLoadServerConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routeup-server.json")
	body := `{"domain":"routeup.dev","listen":":443","public_namespace":"try","db":"/var/lib/routeup/s.db","reserved":["billing"]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if c.Domain != "routeup.dev" || c.Listen != ":443" || c.PublicNamespace != "try" {
		t.Errorf("unexpected config: %+v", c)
	}
	if c.DBPath != "/var/lib/routeup/s.db" || len(c.Reserved) != 1 {
		t.Errorf("unexpected config: %+v", c)
	}
}

func TestLoadServerConfig_Missing(t *testing.T) {
	_, err := LoadServerConfig(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want it to wrap os.ErrNotExist", err)
	}
}

func TestServerConfig_Validate(t *testing.T) {
	valid := ServerConfig{Domain: "routeup.dev", Listen: ":443", DBPath: "s.db", PublicNamespace: "try"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []struct {
		name      string
		cfg       ServerConfig
		errSubstr string
	}{
		{name: "missing domain", cfg: ServerConfig{Listen: ":443", DBPath: "s.db"}, errSubstr: "domain is required"},
		{name: "bad domain", cfg: ServerConfig{Domain: "bad_domain", Listen: ":443", DBPath: "s.db"}, errSubstr: "invalid domain"},
		{name: "bad namespace", cfg: ServerConfig{Domain: "routeup.dev", Listen: ":443", DBPath: "s.db", PublicNamespace: "a.b"}, errSubstr: "public_namespace"},
		{name: "bad reserved", cfg: ServerConfig{Domain: "routeup.dev", Listen: ":443", DBPath: "s.db", Reserved: []string{"ok", "bad_one"}}, errSubstr: "reserved label"},
		{name: "empty listen", cfg: ServerConfig{Domain: "routeup.dev", DBPath: "s.db"}, errSubstr: "listen"},
		{name: "cert mode without key", cfg: ServerConfig{Domain: "routeup.dev", Listen: ":443", DBPath: "s.db", TLSMode: TLSModeCert, TLSCert: "c.pem"}, errSubstr: "tls mode cert"},
		{name: "invalid tls mode", cfg: ServerConfig{Domain: "routeup.dev", Listen: ":443", DBPath: "s.db", TLSMode: "bogus"}, errSubstr: "invalid tls_mode"},
		{name: "invalid acme ca", cfg: ServerConfig{Domain: "routeup.dev", Listen: ":443", DBPath: "s.db", ACMECA: "midway"}, errSubstr: "acme_ca"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}

func TestServerConfig_EffectiveReserved(t *testing.T) {
	c := ServerConfig{Domain: "routeup.dev", PublicNamespace: "try", Reserved: []string{"billing"}}
	r := c.EffectiveReserved()
	for _, l := range []string{"api", "try", "billing"} {
		if !r.Has(l) {
			t.Errorf("EffectiveReserved missing %q", l)
		}
	}
	if r.Has("myapp") {
		t.Errorf("did not expect %q reserved", "myapp")
	}
}
