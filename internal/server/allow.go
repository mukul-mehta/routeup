package server

import (
	"errors"
	"fmt"
	"strings"
)

// AllowPattern is a parsed token grant of the form "*.<suffix>", e.g.
// "*.alice.routeup.dev". The leading "*." is required, and "*" stands for
// exactly one label: the grant covers "foo.alice.routeup.dev" but not the apex
// "alice.routeup.dev" nor any deeper name such as "api.myapp.alice.routeup.dev".
// Public hosts are always a single label under their base, which keeps them
// coverable by one wildcard certificate.
type AllowPattern struct {
	suffix string // the part after "*.", lowercased, e.g. "alice.routeup.dev"
}

// ParseAllowPattern parses a "*.<suffix>" grant. The suffix must be a valid
// dotted DNS name. Input is trimmed and lowercased.
func ParseAllowPattern(s string) (AllowPattern, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return AllowPattern{}, errors.New("empty allow pattern")
	}
	rest, ok := strings.CutPrefix(s, "*.")
	if !ok {
		return AllowPattern{}, fmt.Errorf("allow pattern %q must start with \"*.\"", s)
	}
	if err := validateDNSName(rest); err != nil {
		return AllowPattern{}, fmt.Errorf("invalid allow pattern %q: %w", s, err)
	}
	return AllowPattern{suffix: rest}, nil
}

func (p AllowPattern) Base() string { return p.suffix }

func (p AllowPattern) String() string { return "*." + p.suffix }

// Matches reports whether host falls under the granted suffix: host must be
// exactly one label followed by "."+suffix. Matching is case-insensitive. The
// apex (host == suffix) and any multi-label host do not match — public hosts are
// always one label under their namespace, which keeps them coverable by a single
// wildcard certificate.
func (p AllowPattern) Matches(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if p.suffix == "" || host == "" {
		return false
	}
	prefix, ok := strings.CutSuffix(host, "."+p.suffix)
	if !ok {
		return false
	}
	return prefix != "" && !strings.Contains(prefix, ".")
}
