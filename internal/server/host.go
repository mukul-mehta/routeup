package server

import (
	"errors"
	"fmt"
	"strings"
)

// DeriveTokenHost builds the public host for a token claim: <route>.<base>,
// where base is the suffix granted by the token's allow pattern. Both parts are
// lowercased and are assumed already validated.
func DeriveTokenHost(route, base string) string {
	return strings.ToLower(route) + "." + strings.ToLower(base)
}

// DeriveNamespaceHost builds the public host for a token-less public-namespace
// claim: <label>.<namespace>.<domain>.
func DeriveNamespaceHost(label, namespace, domain string) string {
	return strings.ToLower(label) + "." + strings.ToLower(namespace) + "." + strings.ToLower(domain)
}

// ImmediateChildLabel returns the label of host directly under domain: the
// rightmost label left after stripping ".<domain>". ok is false if host does
// not end in ".<domain>" or nothing remains. For "api.myapp.routeup.dev" under
// "routeup.dev" it returns "myapp"; for "api.alice.routeup.dev" it returns
// "alice", the namespace owner that the reserved-label check guards.
func ImmediateChildLabel(host, domain string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	domain = strings.ToLower(strings.TrimSpace(domain))
	rest, ok := strings.CutSuffix(host, "."+domain)
	if !ok || rest == "" {
		return "", false
	}
	labels := strings.Split(rest, ".")
	last := labels[len(labels)-1]
	if last == "" {
		return "", false
	}
	return last, true
}

// validateDNSName checks that s is a dotted sequence of valid DNS labels, at
// most 253 characters. Unlike route.Parse it allows any suffix, since allow
// patterns and claim hosts legitimately end in the public domain.
func validateDNSName(s string) error {
	if s == "" {
		return errors.New("empty name")
	}
	if len(s) > 253 {
		return fmt.Errorf("name %q exceeds 253 characters", s)
	}
	for _, label := range strings.Split(s, ".") {
		if err := validateLabel(label); err != nil {
			return err
		}
	}
	return nil
}

// validateLabel enforces RFC 1035-style label rules: 1..63 chars of ASCII
// lowercase letters, digits, or hyphen, with no leading or trailing hyphen.
func validateLabel(label string) error {
	if label == "" {
		return errors.New("empty label")
	}
	if len(label) > 63 {
		return fmt.Errorf("label %q exceeds 63 characters", label)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("label %q has leading or trailing hyphen", label)
	}
	for _, r := range label {
		if !isLabelChar(r) {
			return fmt.Errorf("label %q contains invalid character %q", label, r)
		}
	}
	return nil
}

func isLabelChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}
