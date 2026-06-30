package route

import "testing"

func TestMatchTarget_LongestPrefixWithBoundary(t *testing.T) {
	targets := []Target{
		{Path: "/", Port: 3000},
		{Path: "/api", Port: 8080},
	}

	cases := []struct {
		path string
		port int
	}{
		{path: "/", port: 3000},
		{path: "/api", port: 8080},
		{path: "/api/users", port: 8080},
		{path: "/apix", port: 3000},
	}

	for _, tc := range cases {
		got, ok := MatchTarget(targets, tc.path)
		if !ok {
			t.Fatalf("MatchTarget(%q) returned no match", tc.path)
		}
		if got.Port != tc.port {
			t.Fatalf("MatchTarget(%q) port = %d, want %d", tc.path, got.Port, tc.port)
		}
	}
}

func TestPathAllowed_PrefixWildcard(t *testing.T) {
	patterns := []string{"/api/*"}
	if !PathAllowed(patterns, "/api/users") {
		t.Fatal("/api/users should be exposed")
	}
	if PathAllowed(patterns, "/") {
		t.Fatal("/ should not be exposed")
	}
}
