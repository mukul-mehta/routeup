package config

import (
	"strings"
	"testing"
)

// envFor returns a fake Env function backed by a fixed map. Useful for
// exercising the env precedence layer without touching os.Setenv.
func envFor(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// TestResolve exercises the full precedence chain and bare-name resolution.
// errSubstr == "" means the case must succeed; wantRoute / wantPort are checked.
//
// Cases to add (grouped for readability):
//
//	port precedence:
//	  - "flag overrides env and file"
//	      Inputs{PositionalName:"myapp", PortFlag:8080,
//	             Env: envFor({"ROUTEUP_PORT":"9090"}),
//	             File: Config{Port:7070}}
//	      -> wantRoute "myapp", wantPort 8080
//	  - "env overrides file"
//	      Inputs{PositionalName:"myapp",
//	             Env: envFor({"ROUTEUP_PORT":"9090"}),
//	             File: Config{Port:7070}}
//	      -> wantRoute "myapp", wantPort 9090
//	  - "file used when no flag or env"
//	      Inputs{PositionalName:"myapp", File: Config{Port:7070}}
//	      -> wantRoute "myapp", wantPort 7070
	//	  - "missing targets errors"
	//	      Inputs{PositionalName:"myapp"}
	//	      -> errSubstr "no targets"     (or your wording)
//	  - "invalid ROUTEUP_PORT errors"
//	      Inputs{PositionalName:"myapp", Env: envFor({"ROUTEUP_PORT":"notanint"})}
//	      -> errSubstr "ROUTEUP_PORT"
//	  - "out-of-range port errors"
//	      Inputs{PositionalName:"myapp", PortFlag:70000}
//	      -> errSubstr "out of range"
//
//	name precedence and bare-name rule:
//	  - "positional with dot is literal"
//	      Inputs{PositionalName:"api.myapp", PortFlag:8080, File: Config{Name:"other"}}
//	      -> wantRoute "api.myapp"
//	  - "bare positional + file name -> prefixed"
//	      Inputs{PositionalName:"api", PortFlag:8080, File: Config{Name:"myapp"}}
//	      -> wantRoute "api.myapp"
//	  - "bare positional without file name passes through"
//	      Inputs{PositionalName:"foo", PortFlag:8080}
//	      -> wantRoute "foo"
//	  - "no positional uses file name"
//	      Inputs{PortFlag:8080, File: Config{Name:"myapp"}}
//	      -> wantRoute "myapp"
//	  - "positional with dot ignores file name (no scoping)"
//	      Inputs{PositionalName:"api.other", PortFlag:8080, File: Config{Name:"myapp"}}
//	      -> wantRoute "api.other"
//	  - "missing name errors"
//	      Inputs{PortFlag:8080}
//	      -> errSubstr "no route name"     (or your wording)
//
//	validation propagation:
//	  - "invalid name surfaces route.Parse error"
//	      Inputs{PositionalName:"api..myapp", PortFlag:8080}
//	      -> errSubstr "invalid route name"
//
//	combined:
//	  - "everything from File (no positional, no flag, no env)"
//	      Inputs{File: Config{Name:"myapp", Port:8080}}
//	      -> wantRoute "myapp", wantPort 8080
func TestResolve(t *testing.T) {
	cases := []struct {
		name      string
		in        Inputs
		wantRoute string // dotted form; "" means don't check
		wantPort  int    // 0 means don't check
		errSubstr string // "" means expect no error
	}{
		// port precedence
		{
			name: "flag overrides env and file",
			in: Inputs{
				PositionalName: "myapp",
				PortFlag:       8080,
				Env:            envFor(map[string]string{"ROUTEUP_PORT": "9090"}),
				File:           Config{Port: 7070},
			},
			wantRoute: "myapp",
			wantPort:  8080,
		},
		{
			name: "env overrides file",
			in: Inputs{
				PositionalName: "myapp",
				Env:            envFor(map[string]string{"ROUTEUP_PORT": "9090"}),
				File:           Config{Port: 7070},
			},
			wantRoute: "myapp",
			wantPort:  9090,
		},
		{
			name: "file used when no flag or env",
			in: Inputs{
				PositionalName: "myapp",
				File:           Config{Port: 7070},
			},
			wantRoute: "myapp",
			wantPort:  7070,
		},
		{
			name:      "missing targets errors",
			in:        Inputs{PositionalName: "myapp"},
			errSubstr: "no targets",
		},
		{
			name: "invalid ROUTEUP_PORT errors",
			in: Inputs{
				PositionalName: "myapp",
				Env:            envFor(map[string]string{"ROUTEUP_PORT": "notanint"}),
			},
			errSubstr: "ROUTEUP_PORT",
		},
		{
			name: "out-of-range port errors",
			in: Inputs{
				PositionalName: "myapp",
				PortFlag:       70000,
			},
			errSubstr: "out of range",
		},

		// name precedence and bare-name rule
		{
			name: "positional with dot is literal",
			in: Inputs{
				PositionalName: "api.myapp",
				PortFlag:       8080,
				File:           Config{Name: "other"},
			},
			wantRoute: "api.myapp",
			wantPort:  8080,
		},
		{
			name: "bare positional + file name -> prefixed",
			in: Inputs{
				PositionalName: "api",
				PortFlag:       8080,
				File:           Config{Name: "myapp"},
			},
			wantRoute: "api.myapp",
			wantPort:  8080,
		},
		{
			name: "bare positional without file name passes through",
			in: Inputs{
				PositionalName: "foo",
				PortFlag:       8080,
			},
			wantRoute: "foo",
			wantPort:  8080,
		},
		{
			name: "no positional uses file name",
			in: Inputs{
				PortFlag: 8080,
				File:     Config{Name: "myapp"},
			},
			wantRoute: "myapp",
			wantPort:  8080,
		},
		{
			name: "positional with dot ignores file name (no scoping)",
			in: Inputs{
				PositionalName: "api.other",
				PortFlag:       8080,
				File:           Config{Name: "myapp"},
			},
			wantRoute: "api.other",
			wantPort:  8080,
		},
		{
			name:      "missing name errors",
			in:        Inputs{PortFlag: 8080},
			errSubstr: "no route name",
		},
		{
			name: "ROUTEUP_NAME used when no positional and no file name",
			in: Inputs{
				PortFlag: 8080,
				Env:      envFor(map[string]string{"ROUTEUP_NAME": "envname"}),
			},
			wantRoute: "envname",
			wantPort:  8080,
		},
		{
			name: "ROUTEUP_NAME overrides file name when no positional",
			in: Inputs{
				PortFlag: 8080,
				Env:      envFor(map[string]string{"ROUTEUP_NAME": "envname"}),
				File:     Config{Name: "filename"},
			},
			wantRoute: "envname",
			wantPort:  8080,
		},
		{
			name: "positional beats ROUTEUP_NAME",
			in: Inputs{
				PositionalName: "cli",
				PortFlag:       8080,
				Env:            envFor(map[string]string{"ROUTEUP_NAME": "envname"}),
				File:           Config{Name: "filename"},
			},
			wantRoute: "cli.filename",
			wantPort:  8080,
		},

		// validation propagation
		{
			name: "invalid name surfaces route.Parse error",
			in: Inputs{
				PositionalName: "api..myapp",
				PortFlag:       8080,
			},
			errSubstr: "invalid route name",
		},

		// combined
		{
			name:      "everything from File",
			in:        Inputs{File: Config{Name: "myapp", Port: 8080}},
			wantRoute: "myapp",
			wantPort:  8080,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.in)
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("Resolve unexpected error: %v", err)
				}
				if tc.wantRoute != "" && got.Route.String() != tc.wantRoute {
					t.Errorf("Route = %q, want %q", got.Route.String(), tc.wantRoute)
				}
				if tc.wantPort != 0 && got.Port != tc.wantPort {
					t.Errorf("Port = %d, want %d", got.Port, tc.wantPort)
				}
				return
			}
			if err == nil {
				t.Fatalf("Resolve expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("Resolve error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}
