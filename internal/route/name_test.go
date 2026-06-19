package route

import (
	"slices"
	"strings"
	"testing"
)

// TestParse_Valid covers inputs that Parse should accept.
func TestParse_Valid(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		labels []string
	}{
		{name: "simple", in: "myapp", labels: []string{"myapp"}},
		{name: "two labels", in: "api.myapp", labels: []string{"api", "myapp"}},
		{name: "five labels", in: "a.b.c.d.e", labels: []string{"a", "b", "c", "d", "e"}},
		{name: "uppercase normalized", in: "API.MyApp", labels: []string{"api", "myapp"}},
		{name: "middle hyphen", in: "my-app", labels: []string{"my-app"}},
		{name: "digit leading label", in: "123myapp", labels: []string{"123myapp"}},
		{name: "63-char label", in: strings.Repeat("a", 63), labels: []string{strings.Repeat("a", 63)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tc.in, err)
			}
			if !slices.Equal(got.Labels, tc.labels) {
				t.Errorf("Parse(%q) labels = %v, want %v", tc.in, got.Labels, tc.labels)
			}
		})
	}
}

// TestParse_Invalid covers inputs that Parse should reject.
func TestParse_Invalid(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		errSubstr string
	}{
		{name: "empty", in: "", errSubstr: "empty"},
		{name: "just dot", in: ".", errSubstr: "empty"},
		{name: "leading dot", in: ".myapp", errSubstr: "empty"},
		{name: "trailing dot", in: "myapp.", errSubstr: "empty"},
		{name: "double dot", in: "api..myapp", errSubstr: "empty"},
		{name: "leading hyphen", in: "-myapp", errSubstr: "hyphen"},
		{name: "trailing hyphen", in: "myapp-", errSubstr: "hyphen"},
		{name: "underscore", in: "my_app", errSubstr: "invalid character"},
		{name: "non-ASCII", in: "café", errSubstr: "invalid character"},
		{name: "is local suffix", in: "localhost", errSubstr: "reserved suffix"},
		{name: "ends with local suffix", in: "api.localhost", errSubstr: "reserved suffix"},
		{name: "is public suffix", in: "routeup.dev", errSubstr: "reserved suffix"},
		{name: "ends with public suffix", in: "foo.routeup.dev", errSubstr: "reserved suffix"},
		{name: "label too long", in: strings.Repeat("a", 64), errSubstr: "63"},
		{name: "total too long", in: strings.Repeat("a", 254), errSubstr: "253"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.in)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tc.in)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tc.in, err.Error(), tc.errSubstr)
			}
		})
	}
}

// TestName_Hosts checks LocalHost and PublicHost on valid names.
func TestName_Hosts(t *testing.T) {
	cases := []struct {
		in     string
		local  string
		public string
	}{
		{in: "myapp", local: "myapp.localhost", public: "myapp.routeup.dev"},
		{in: "api.myapp", local: "api.myapp.localhost", public: "api.myapp.routeup.dev"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			n, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got := n.LocalHost(); got != tc.local {
				t.Errorf("LocalHost() = %q, want %q", got, tc.local)
			}
			if got := n.PublicHost(); got != tc.public {
				t.Errorf("PublicHost() = %q, want %q", got, tc.public)
			}
		})
	}
}
