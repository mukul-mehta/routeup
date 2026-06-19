package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStripScope covers the npm-scope prefix stripping helper directly so
// failures surface here rather than through the full JSON pipeline.
func TestStripScope(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "myapp", want: "myapp"},
		{in: "@org/myapp", want: "myapp"},
		{in: "@scope/foo-bar", want: "foo-bar"},
		{in: "@org/sub/path", want: "sub/path"},
		{in: "@org", want: "@org"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := stripScope(tc.in); got != tc.want {
				t.Errorf("stripScope(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestLoadPackageJSON_MissingFile asserts the missing-file contract: callers
// can use errors.Is(err, os.ErrNotExist) to distinguish "no config" from
// other failures.
func TestLoadPackageJSON_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.json")

	_, _, err := LoadPackageJSON(path)
	if err == nil {
		t.Fatal("LoadPackageJSON on missing file: expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadPackageJSON err = %v, want errors.Is(err, os.ErrNotExist)", err)
	}
}

// TestLoadPackageJSON walks package.json content through the loader.
func TestLoadPackageJSON(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		want         Config
		wantHasBlock bool
		errSubstr    string
	}{
		// Valid cases
		{name: "empty object", content: `{}`, want: Config{}, wantHasBlock: false},
		{name: "top-level name only", content: `{"name":"app-web"}`, want: Config{}, wantHasBlock: false},
		{name: "routeup block name only", content: `{"routeup":{"name":"myapp"}}`, want: Config{Name: "myapp"}, wantHasBlock: true},
		{name: "routeup block name+port", content: `{"routeup":{"name":"myapp","port":8080}}`, want: Config{Name: "myapp", Port: 8080}, wantHasBlock: true},
		{name: "scoped name stripped", content: `{"routeup":{"name":"@org/myapp"}}`, want: Config{Name: "myapp"}, wantHasBlock: true},
		{name: "sibling top-level name tolerated", content: `{"name":"app-web","routeup":{"name":"myapp"}}`, want: Config{Name: "myapp"}, wantHasBlock: true},
		{name: "routeup null treated as absent", content: `{"routeup":null}`, want: Config{}, wantHasBlock: false},

		// Invalid cases
		{name: "malformed JSON", content: `{not json`, errSubstr: "could not parse"},
		{name: "routeup block not an object", content: `{"routeup":"not an object"}`, errSubstr: "could not parse routeup block"},
		{name: "invalid name in block", content: `{"routeup":{"name":"api..myapp"}}`, errSubstr: "could not validate"},
		{name: "invalid port in block", content: `{"routeup":{"port":-1}}`, errSubstr: "could not validate"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTmpFile(t, "package.json", tc.content)

			got, gotHasBlock, err := LoadPackageJSON(path)
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("LoadPackageJSON unexpected error: %v", err)
				}
				if got != tc.want {
					t.Errorf("LoadPackageJSON config = %+v, want %+v", got, tc.want)
				}
				if gotHasBlock != tc.wantHasBlock {
					t.Errorf("LoadPackageJSON hasBlock = %v, want %v", gotHasBlock, tc.wantHasBlock)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadPackageJSON expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("LoadPackageJSON error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}
