package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mukul-mehta/routeup/internal/route"
)

// TestConfig_Validate runs valid and invalid Config values through Validate.
func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name      string
		cfg       Config
		errSubstr string
	}{
		// Valid cases
		{name: "empty config", cfg: Config{}, errSubstr: ""},
		{name: "name-only config", cfg: Config{Name: "myapp"}, errSubstr: ""},
		{name: "port-only config", cfg: Config{Port: 8080}, errSubstr: ""},
		{name: "name+port config", cfg: Config{Name: "myapp", Port: 8080}, errSubstr: ""},
		{name: "targets config", cfg: Config{Targets: []route.Target{{Path: "/", Port: 3000}, {Path: "/api", Port: 8080}}}, errSubstr: ""},

		// Invalid cases
		{name: "double dot in name", cfg: Config{Name: "api..myapp"}, errSubstr: "invalid name"},
		{name: "name ends in reserved suffix", cfg: Config{Name: "myapp.localhost"}, errSubstr: "invalid name"},
		{name: "negative port", cfg: Config{Port: -1}, errSubstr: "port -1 out of range"},
		{name: "port above limit", cfg: Config{Port: 72000}, errSubstr: "port 72000 out of range"},
		{name: "duplicate root port", cfg: Config{Port: 3000, Targets: []route.Target{{Path: "/", Port: 8080}}}, errSubstr: "port and targets path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("Validate() error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}

// TestLoadRouteupJSON_MissingFile asserts the contract: callers can use
// errors.Is(err, os.ErrNotExist) to distinguish "no config" from real failures.
func TestLoadRouteupJSON_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	_, err := LoadRouteupJSON(path)
	if err == nil {
		t.Fatal("LoadRouteupJSON on missing file: expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadRouteupJSON err = %v, want errors.Is(err, os.ErrNotExist)", err)
	}
}

// TestLoadRouteupJSON walks routeup.json content through the decoder and Validate.
func TestLoadRouteupJSON(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		want      Config
		errSubstr string
	}{
		// Valid cases
		{name: "valid name+port", content: `{"name":"myapp","port":8080}`, want: Config{Name: "myapp", Port: 8080}, errSubstr: ""},
		{name: "name-only", content: `{"name":"myapp"}`, want: Config{Name: "myapp"}, errSubstr: ""},
		{name: "port-only", content: `{"port": 8080}`, want: Config{Port: 8080}, errSubstr: ""},
		{name: "targets", content: `{"name":"myapp","targets":[{"path":"/","port":3000},{"path":"/api","port":8080}]}`, want: Config{Name: "myapp", Targets: []route.Target{{Path: "/", Port: 3000}, {Path: "/api", Port: 8080}}}, errSubstr: ""},
		{name: "valid name+port+unknown field", content: `{"name":"myapp","port":8080,"paths":["/api/webhooks"]}`, want: Config{Name: "myapp", Port: 8080}, errSubstr: ""},

		// Invalid cases
		{name: "malformed JSON", content: `{"name":"myapp"`, want: Config{Name: "myapp", Port: 8080}, errSubstr: "could not parse"},
		{name: "invalid name", content: `{"name": "api..myapp"}`, want: Config{}, errSubstr: "could not validate"},
		{name: "invalid port", content: `{"port": -42}`, want: Config{}, errSubstr: "could not validate"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTmpFile(t, "routeup.json", tc.content)

			got, err := LoadRouteupJSON(path)
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("LoadRouteupJSON unexpected error: %v", err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("LoadRouteupJSON = %+v, want %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadRouteupJSON expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("LoadRouteupJSON error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}

func writeTmpFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write tmp file %s: %v", path, err)
	}
	return path
}
