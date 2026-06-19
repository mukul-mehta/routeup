package route

import (
	"errors"
	"fmt"
	"strings"
)

// Name is a validated, dotted route name. Labels are stored lowercase, in
// the order they appeared (left-to-right). Zero value is not valid; use Parse.
type Name struct {
	Labels []string
}

// Parse splits, normalizes, and validates a dotted route name.
//
// Rules:
//   - Must contain at least one label.
//   - Each label: ASCII letters/digits/hyphens, 1-63 chars, no leading or
//     trailing hyphen. (RFC 1035-style DNS labels, lowercase only.)
//   - Total dotted form must not exceed 253 characters.
//   - Empty labels (e.g. "api..myapp"), leading dots (".myapp"), or
//     trailing dots ("myapp.") are rejected.
//   - The input is rejected if it ends with "." + LocalSuffix or "." + PublicSuffix
//     (or equals the suffix exactly).
//
// Input is lowercased before validation, so "API.MyApp" is accepted and stored
// as ["api", "myapp"].
func Parse(s string) (Name, error) {
	if s == "" {
		return Name{}, errors.New("empty route name")
	}

	s = strings.ToLower(s)

	if len(s) > 253 {
		return Name{}, fmt.Errorf("route name %q exceeds 253 characters", s)
	}

	if s == LocalSuffix || strings.HasSuffix(s, "."+LocalSuffix) {
		return Name{}, fmt.Errorf("route name %q ends with reserved suffix %q", s, LocalSuffix)
	}
	if s == PublicSuffix || strings.HasSuffix(s, "."+PublicSuffix) {
		return Name{}, fmt.Errorf("route name %q ends with reserved suffix %q", s, PublicSuffix)
	}

	labels := strings.Split(s, ".")
	for _, label := range labels {
		if err := validateLabel(label); err != nil {
			return Name{}, fmt.Errorf("invalid route name %q: %w", s, err)
		}
	}

	return Name{Labels: labels}, nil
}

// String returns the dotted form, e.g. "api.myapp".
func (n Name) String() string {
	return strings.Join(n.Labels, ".")
}

// LocalHost returns the local hostname: <name>.localhost.
func (n Name) LocalHost() string {
	return n.String() + "." + LocalSuffix
}

// PublicHost returns the public hostname: <name>.routeup.dev.
func (n Name) PublicHost() string {
	return n.String() + "." + PublicSuffix
}

// validateLabel enforces the per-label rules described on Parse.
func validateLabel(label string) error {
	if len(label) == 0 {
		return errors.New("empty label")
	}
	if len(label) > 63 {
		return fmt.Errorf("label %q exceeds 63 characters", label)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("label %q has leading or trailing hyphen", label)
	}
	for _, r := range label {
		if !isValidLabelChar(r) {
			return fmt.Errorf("label %q contains invalid character %q", label, r)
		}
	}
	return nil
}

// isValidLabelChar reports whether r is permitted inside a route label:
// ASCII lowercase letter, digit, or hyphen.
func isValidLabelChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}
