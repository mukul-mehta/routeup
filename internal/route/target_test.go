package route

import (
	"strings"
	"testing"
)

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

func TestPathNormalizationRejectsControlCharacters(t *testing.T) {
	if _, err := NormalizeTarget(Target{Path: "/api\x1b[2J", Port: 8080}); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("target error = %v, want control-character rejection", err)
	}
	if _, err := NormalizePathPatterns([]string{"/hooks\n/*"}); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("expose error = %v, want control-character rejection", err)
	}
}

func TestNormalizePathPatternsRejectsInteriorWildcard(t *testing.T) {
	if _, err := NormalizePathPatterns([]string{"/api/*/private/*"}); err == nil {
		t.Fatal("interior wildcard accepted")
	}
}
