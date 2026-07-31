package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mkTree creates a fresh temporary directory and writes each (relPath, content)
// entry below it, creating intermediate directories as needed. It returns the
// absolute path of the temp directory root.
func mkTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

// TestDiscover exercises current-directory discovery and source precedence.
func TestDiscover(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		startDir   string // relative to the temp root
		wantSource Source
		wantName   string // "" means don't check
		errSubstr  string // "" means expect no error
	}{
		// none-found
		{
			name:       "empty tree",
			files:      map[string]string{},
			startDir:   ".",
			wantSource: SourceNone,
		},
		{
			name: "package.json without routeup block",
			files: map[string]string{
				"package.json": `{"name":"app-web"}`,
			},
			startDir:   ".",
			wantSource: SourceNone,
		},
		{
			name: "parent config is not discovered",
			files: map[string]string{
				"routeup.json": `{"name":"parent"}`,
				"nested/.keep": "",
			},
			startDir:   "nested",
			wantSource: SourceNone,
		},

		// routeup.json
		{
			name: "routeup.json at startDir",
			files: map[string]string{
				"routeup.json": `{"name":"myapp"}`,
			},
			startDir:   ".",
			wantSource: SourceRouteupJSON,
			wantName:   "myapp",
		},

		// package.json
		{
			name: "package.json with routeup block at startDir",
			files: map[string]string{
				"package.json": `{"routeup":{"name":"myapp"}}`,
			},
			startDir:   ".",
			wantSource: SourcePackageJSON,
			wantName:   "myapp",
		},

		// precedence in same dir
		{
			name: "routeup.json beats package.json in same dir",
			files: map[string]string{
				"routeup.json": `{"name":"win"}`,
				"package.json": `{"routeup":{"name":"lose"}}`,
			},
			startDir:   ".",
			wantSource: SourceRouteupJSON,
			wantName:   "win",
		},

		// error propagation
		{
			name: "malformed routeup.json returns an error",
			files: map[string]string{
				"routeup.json": `{not json`,
			},
			startDir:  ".",
			errSubstr: "could not parse",
		},
		{
			name: "invalid routeup.json returns an error",
			files: map[string]string{
				"routeup.json": `{"port":-1}`,
			},
			startDir:  ".",
			errSubstr: "could not validate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := mkTree(t, tc.files)
			start := filepath.Join(root, tc.startDir)

			got, err := Discover(start)
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("Discover unexpected error: %v", err)
				}
				if got.Source != tc.wantSource {
					t.Errorf("Source = %q, want %q", got.Source, tc.wantSource)
				}
				if tc.wantName != "" && got.Config.Name != tc.wantName {
					t.Errorf("Config.Name = %q, want %q", got.Config.Name, tc.wantName)
				}
				if got.Source != SourceNone && got.Path == "" {
					t.Errorf("Path is empty but Source = %q", got.Source)
				}
				if got.Source == SourceNone && (got.Path != "" || !reflect.DeepEqual(got.Config, Config{})) {
					t.Errorf("SourceNone result must have empty Path and zero Config; got %+v", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("Discover expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("Discover error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}
