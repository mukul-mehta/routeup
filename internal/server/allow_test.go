package server

import (
	"strings"
	"testing"
)

func TestParseAllowPattern_Valid(t *testing.T) {
	cases := []struct {
		in   string
		base string
	}{
		{in: "*.routeup.dev", base: "routeup.dev"},
		{in: "*.alice.routeup.dev", base: "alice.routeup.dev"},
		{in: "*.team-x.routeup.dev", base: "team-x.routeup.dev"},
		{in: "  *.ALICE.RouteUp.Dev  ", base: "alice.routeup.dev"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			p, err := ParseAllowPattern(tc.in)
			if err != nil {
				t.Fatalf("ParseAllowPattern(%q) unexpected error: %v", tc.in, err)
			}
			if p.Base() != tc.base {
				t.Errorf("Base() = %q, want %q", p.Base(), tc.base)
			}
			if want := "*." + tc.base; p.String() != want {
				t.Errorf("String() = %q, want %q", p.String(), want)
			}
		})
	}
}

func TestParseAllowPattern_Invalid(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		errSubstr string
	}{
		{name: "empty", in: "", errSubstr: "empty"},
		{name: "no wildcard prefix", in: "routeup.dev", errSubstr: "must start"},
		{name: "bare wildcard", in: "*.", errSubstr: "empty"},
		{name: "double dot", in: "*.foo..bar", errSubstr: "empty label"},
		{name: "trailing dot", in: "*.routeup.dev.", errSubstr: "empty label"},
		{name: "leading hyphen", in: "*.-bad.dev", errSubstr: "hyphen"},
		{name: "underscore", in: "*.foo_bar.dev", errSubstr: "invalid character"},
		{name: "mid wildcard not allowed", in: "*.foo.*.dev", errSubstr: "invalid character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAllowPattern(tc.in)
			if err == nil {
				t.Fatalf("ParseAllowPattern(%q) expected error, got nil", tc.in)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}

func TestAllowPattern_Matches(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		// one label under the grant is matched
		{pattern: "*.alice.routeup.dev", host: "foo.alice.routeup.dev", want: true},
		// multi-label is NOT matched (public hosts are one label under a base)
		{pattern: "*.alice.routeup.dev", host: "api.myapp.alice.routeup.dev", want: false},
		// apex of the grant is not claimable
		{pattern: "*.alice.routeup.dev", host: "alice.routeup.dev", want: false},
		// a sibling namespace is off-limits
		{pattern: "*.alice.routeup.dev", host: "bob.routeup.dev", want: false},
		// label-boundary safety: no dot before the suffix
		{pattern: "*.alice.routeup.dev", host: "xalice.routeup.dev", want: false},
		// root grant, single label
		{pattern: "*.routeup.dev", host: "anything.routeup.dev", want: true},
		// root grant does not cover two-level hosts
		{pattern: "*.routeup.dev", host: "a.b.routeup.dev", want: false},
		{pattern: "*.routeup.dev", host: "routeup.dev", want: false},
		{pattern: "*.routeup.dev", host: "evil-routeup.dev", want: false},
		// case-insensitive
		{pattern: "*.alice.routeup.dev", host: "API.Alice.RouteUp.Dev", want: true},
		// empty host
		{pattern: "*.routeup.dev", host: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"|"+tc.host, func(t *testing.T) {
			p, err := ParseAllowPattern(tc.pattern)
			if err != nil {
				t.Fatalf("ParseAllowPattern(%q): %v", tc.pattern, err)
			}
			if got := p.Matches(tc.host); got != tc.want {
				t.Errorf("Matches(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}
