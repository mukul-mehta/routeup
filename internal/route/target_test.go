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
	if !PathAllowed(patterns, "/api/users", "/api/users") {
		t.Fatal("/api/users should be exposed")
	}
	if PathAllowed(patterns, "/", "/") {
		t.Fatal("/ should not be exposed")
	}
}

func TestPathAllowedRejectsAmbiguousPaths(t *testing.T) {
	patterns := []string{"/api/*"}
	tests := []struct {
		name        string
		path        string
		escapedPath string
	}{
		{name: "dot segment", path: "/api/../admin", escapedPath: "/api/../admin"},
		{name: "encoded dot segment", path: "/api/../admin", escapedPath: "/api/%2e%2e/admin"},
		{name: "encoded slash", path: "/api/admin", escapedPath: "/api%2fadmin"},
		{name: "double encoded slash", path: "/api/%2fadmin", escapedPath: "/api/%252fadmin"},
		{name: "deep encoded dot segment", path: "/api/%25252e%25252e/admin", escapedPath: "/api/%2525252e%2525252e/admin"},
		{name: "backslash", path: `/api\..\admin`, escapedPath: "/api%5c..%5cadmin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if PathAllowed(patterns, test.path, test.escapedPath) {
				t.Fatalf("PathAllowed(%q, %q) = true, want false", test.path, test.escapedPath)
			}
		})
	}
}

func TestPathAllowedAllPathsDoesNotApplyRestrictionPolicy(t *testing.T) {
	if !PathAllowed([]string{"/*"}, "/files/a/b", "/files/a%2fb") {
		t.Fatal("/* should expose all paths, including encoded path forms")
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
