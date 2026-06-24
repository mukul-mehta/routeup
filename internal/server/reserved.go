package server

import "strings"

// DefaultReservedLabels are the subdomain labels the server refuses to let
// clients claim directly under the public domain. They are reserved for the
// server itself (control endpoints, a future operator UI), for conventions
// users assume are taken, and for the public namespace. Operators extend this
// set via config, and the chosen public-namespace label is added automatically.
func DefaultReservedLabels() []string {
	return []string{"api", "admin", "app", "dashboard", "dev", "docs", "get", "status", "www", "try"}
}

// ReservedSet is a case-insensitive set of reserved subdomain labels.
type ReservedSet map[string]struct{}

// NewReservedSet builds a set from the default labels plus any extras (operator
// config entries and the public-namespace label). Blank extras are ignored;
// labels are trimmed and lowercased.
func NewReservedSet(extra ...string) ReservedSet {
	s := make(ReservedSet)
	for _, l := range DefaultReservedLabels() {
		s[l] = struct{}{}
	}
	for _, l := range extra {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" {
			s[l] = struct{}{}
		}
	}
	return s
}

// Has reports whether label is reserved. Matching is case-insensitive.
func (s ReservedSet) Has(label string) bool {
	_, ok := s[strings.ToLower(strings.TrimSpace(label))]
	return ok
}
