package route

import (
	"errors"
	"fmt"
	"strings"
)

// Target is one local upstream behind a route. Path is a path prefix such as
// "/" or "/api"; Port is the localhost port to proxy matching requests to.
type Target struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

// NormalizeTarget validates t and returns its canonical form.
func NormalizeTarget(t Target) (Target, error) {
	path, err := normalizeTargetPath(t.Path)
	if err != nil {
		return Target{}, err
	}
	if t.Port < 1 || t.Port > 65535 {
		return Target{}, fmt.Errorf("port %d out of range [1, 65535]", t.Port)
	}
	return Target{Path: path, Port: t.Port}, nil
}

// NormalizeTargets validates targets, canonicalizes paths, and rejects duplicate
// path prefixes. It preserves input order after normalization.
func NormalizeTargets(targets []Target) ([]Target, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(targets))
	out := make([]Target, 0, len(targets))
	for _, target := range targets {
		normalized, err := NormalizeTarget(target)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized.Path]; ok {
			return nil, fmt.Errorf("duplicate target path %q", normalized.Path)
		}
		seen[normalized.Path] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// MatchTarget returns the longest target path prefix matching requestPath.
func MatchTarget(targets []Target, requestPath string) (Target, bool) {
	if requestPath == "" {
		requestPath = "/"
	}
	if !strings.HasPrefix(requestPath, "/") {
		requestPath = "/" + requestPath
	}

	var match Target
	matched := false
	for _, target := range targets {
		if !targetPathMatches(target.Path, requestPath) {
			continue
		}
		if !matched || len(target.Path) > len(match.Path) {
			match = target
			matched = true
		}
	}
	return match, matched
}

// PrimaryPort returns the root target port when present, otherwise the first
// configured target's port. It returns zero when no targets exist.
func PrimaryPort(targets []Target) int {
	for _, target := range targets {
		if target.Path == "/" {
			return target.Port
		}
	}
	if len(targets) == 0 {
		return 0
	}
	return targets[0].Port
}

// NormalizePathPatterns validates public exposure path patterns. A pattern may
// be an exact path such as "/callback" or a trailing-wildcard prefix such as
// "/api/*". The special pattern "/*" exposes all paths.
func NormalizePathPatterns(patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		normalized, err := normalizePathPattern(pattern)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			return nil, fmt.Errorf("duplicate expose path %q", normalized)
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// PathAllowed reports whether requestPath is allowed by patterns. An empty
// pattern list means all paths are exposed.
func PathAllowed(patterns []string, requestPath string) bool {
	if len(patterns) == 0 {
		return true
	}
	if requestPath == "" {
		requestPath = "/"
	}
	if !strings.HasPrefix(requestPath, "/") {
		requestPath = "/" + requestPath
	}
	for _, pattern := range patterns {
		if pattern == "/*" {
			return true
		}
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "/*")
			if targetPathMatches(prefix, requestPath) {
				return true
			}
			continue
		}
		if requestPath == pattern {
			return true
		}
	}
	return false
}

func normalizeTargetPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("target path is required")
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("target path %q must start with /", path)
	}
	if strings.ContainsAny(path, "?#*") {
		return "", fmt.Errorf("target path %q must not contain ?, #, or *", path)
	}
	return trimPathSlash(path), nil
}

func normalizePathPattern(pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", errors.New("expose path is required")
	}
	if pattern == "/*" {
		return pattern, nil
	}
	if !strings.HasPrefix(pattern, "/") {
		return "", fmt.Errorf("expose path %q must start with /", pattern)
	}
	if strings.ContainsAny(pattern, "?#") {
		return "", fmt.Errorf("expose path %q must not contain ? or #", pattern)
	}
	if strings.Contains(pattern, "*") && !strings.HasSuffix(pattern, "/*") {
		return "", fmt.Errorf("expose path %q may only use * as a trailing /*", pattern)
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		if prefix == "" {
			return "/*", nil
		}
		prefix = trimPathSlash(prefix)
		if prefix == "/" {
			return "/*", nil
		}
		return prefix + "/*", nil
	}
	return trimPathSlash(pattern), nil
}

func targetPathMatches(prefix, requestPath string) bool {
	if prefix == "/" {
		return true
	}
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

func trimPathSlash(path string) string {
	for len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}
