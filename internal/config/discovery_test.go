package config

import (
	"os"
	"path/filepath"
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

// TestDiscover exercises the walk-up algorithm with assorted directory layouts.
// errSubstr == "" means the case must succeed; wantSource/wantName are checked.
// startDir is interpreted relative to the temp root.
//
// Cases to add:
//
//	none-found:
//	  - "empty tree"
//	      files: {}
//	      startDir: "."
//	      -> SourceNone, name ""
//	  - "package.json without routeup block, nothing upstream"
//	      files: {"package.json": `{"name":"app-web"}`}
//	      startDir: "."
//	      -> SourceNone, name ""
//
//	routeup.json:
//	  - "routeup.json at startDir"
//	      files: {"routeup.json": `{"name":"myapp"}`}
//	      startDir: "."
//	      -> SourceRouteupJSON, name "myapp"
//
//	package.json:
//	  - "package.json with routeup block at startDir"
//	      files: {"package.json": `{"routeup":{"name":"myapp"}}`}
//	      startDir: "."
//	      -> SourcePackageJSON, name "myapp"
//
//	walk-up:
//	  - "package.json without block, routeup.json upstream"
//	      files: {
//	          "a/b/package.json": `{"name":"app"}`,
//	          "a/routeup.json":   `{"name":"upstream"}`,
//	      }
//	      startDir: "a/b"
//	      -> SourceRouteupJSON, name "upstream"
//
//	precedence in same dir:
//	  - "routeup.json beats package.json"
//	      files: {
//	          "routeup.json": `{"name":"win"}`,
//	          "package.json": `{"routeup":{"name":"lose"}}`,
//	      }
//	      startDir: "."
//	      -> SourceRouteupJSON, name "win"
//
//	closest wins:
//	  - "inner routeup.json beats outer"
//	      files: {
//	          "routeup.json":     `{"name":"outer"}`,
//	          "a/b/routeup.json": `{"name":"inner"}`,
//	      }
//	      startDir: "a/b"
//	      -> SourceRouteupJSON, name "inner"
//
//	error propagation:
//	  - "malformed routeup.json on the path stops the walk"
//	      files: {"routeup.json": `{not json`}
//	      startDir: "."
//	      -> errSubstr "could not parse"
//	  - "validation failure on the path stops the walk"
//	      files: {"routeup.json": `{"port":-1}`}
//	      startDir: "."
//	      -> errSubstr "could not validate"
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
			name: "package.json without routeup block and nothing upstream",
			files: map[string]string{
				"package.json": `{"name":"app-web"}`,
			},
			startDir:   ".",
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
			name: "malformed routeup.json on the path stops the walk",
			files: map[string]string{
				"routeup.json": `{not json`,
			},
			startDir:  ".",
			errSubstr: "could not parse",
		},
		{
			name: "validation failure on the path stops the walk",
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
				if got.Source == SourceNone && (got.Path != "" || got.Config != (Config{})) {
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
