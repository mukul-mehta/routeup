package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mukul-mehta/routeup/internal/route"
)

// AuthzError is a rejection carrying the HTTP status to return
type AuthzError struct {
	Status int
	Reason string
}

func (e *AuthzError) Error() string { return e.Reason }

// Decision is the outcome of a successful authorization: the resolved public
// host, how it was reached, and the namespace base it sits under (so the caller
// can ensure a wildcard certificate).
type Decision struct {
	Host      string
	Mode      ClaimKind
	Ephemeral bool
	Base      string // e.g. "routeup.dev" (root) or "mukul.routeup.dev"
	claimReq  ClaimRequest
}

// ClaimRequest returns the request the caller passes to Store.Claim to hold the
// decided host.
func (d Decision) ClaimRequest() ClaimRequest { return d.claimReq }

// ClaimAttempt is a request to claim a public route. An empty TokenSecret takes
// the public-namespace (try) path. Random requests a server-assigned label.
type ClaimAttempt struct {
	TokenSecret string
	Route       string
	Random      bool
}

// Authorizer resolves a ClaimAttempt to a public host or a typed rejection.
//
// Every public host is one label under a namespace base: <label>.<base>. There
// are three tiers, all expressed by the token's allow patterns:
//
//	try         no token        <label>.try.<domain>
//	root        *.<domain>      <label>.<domain>
//	namespace   *.<ns>.<domain> <label>.<ns>.<domain>
//
// Reserved names protect only the root tier; inside an owned namespace the
// tenant may use any label (api.mukul.<domain> is mukul's).
type Authorizer struct {
	cfg      ServerConfig
	reserved ReservedSet
	store    *Store
}

// NewAuthorizer builds an Authorizer for cfg backed by store.
func NewAuthorizer(cfg ServerConfig, store *Store) *Authorizer {
	return &Authorizer{cfg: cfg, reserved: cfg.EffectiveReserved(), store: store}
}

// Authorize resolves attempt to a Decision or returns an *AuthzError (mappable
// to an HTTP status) or a real error for internal failures.
func (a *Authorizer) Authorize(ctx context.Context, attempt ClaimAttempt) (Decision, error) {
	if attempt.TokenSecret != "" {
		return a.authorizeToken(ctx, attempt)
	}
	return a.authorizeNamespace(ctx, attempt)
}

func (a *Authorizer) authorizeToken(ctx context.Context, attempt ClaimAttempt) (Decision, error) {
	tok, err := a.store.VerifyToken(ctx, attempt.TokenSecret)
	if err != nil {
		if errors.Is(err, ErrTokenInvalid) {
			return Decision{}, &AuthzError{Status: http.StatusUnauthorized, Reason: "invalid or revoked token"}
		}
		return Decision{}, fmt.Errorf("verify token: %w", err)
	}

	label, aerr := resolvePublicLabel(attempt)
	if aerr != nil {
		return Decision{}, aerr
	}

	nsLabels, err := a.namespaceLabels(ctx)
	if err != nil {
		return Decision{}, fmt.Errorf("load namespaces: %w", err)
	}

	host, base, aerr := a.placeUnderToken(label, tok, nsLabels)
	if aerr != nil {
		return Decision{}, aerr
	}

	req := ClaimRequest{Host: host, Kind: ClaimToken, TokenID: tok.ID}
	return Decision{Host: host, Mode: ClaimToken, Base: base, claimReq: req}, nil
}

// placeUnderToken finds the first allow pattern that can host label and returns
// the resolved host and its namespace base. nsLabels is the set of currently
// granted namespace labels, reserved at the root tier.
func (a *Authorizer) placeUnderToken(label string, tok *Token, nsLabels ReservedSet) (string, string, *AuthzError) {
	for _, p := range tok.Patterns {
		base := p.Base()
		if base != a.cfg.Domain && !strings.HasSuffix(base, "."+a.cfg.Domain) {
			continue // pattern is outside this server's domain
		}
		if a.placementAllowed(label, base, nsLabels) {
			return DeriveTokenHost(label, base), base, nil
		}
	}
	return "", "", &AuthzError{
		Status: http.StatusForbidden,
		Reason: "route cannot be placed under the token's namespaces (reserved or out of scope)",
	}
}

// placementAllowed reports whether label may be claimed under base.
//
//   - Root tier (base == domain): label must not be a built-in reserved name or
//     an active namespace label.
//   - Namespace tier (base == <ns>.domain): the namespace label itself must not
//     be a reserved root name; the leaf label is the tenant's to choose.
func (a *Authorizer) placementAllowed(label, base string, nsLabels ReservedSet) bool {
	if base == a.cfg.Domain {
		return !a.reserved.Has(label) && !nsLabels.Has(label)
	}
	nsLabel, ok := ImmediateChildLabel(base, a.cfg.Domain)
	if !ok {
		return false
	}
	return !a.reserved.Has(nsLabel)
}

func (a *Authorizer) authorizeNamespace(ctx context.Context, attempt ClaimAttempt) (Decision, error) {
	if a.cfg.PublicNamespace == "" {
		return Decision{}, &AuthzError{
			Status: http.StatusUnauthorized,
			Reason: "no token provided and this server has no public namespace",
		}
	}

	label, aerr := resolvePublicLabel(attempt)
	if aerr != nil {
		return Decision{}, aerr
	}

	host := DeriveNamespaceHost(label, a.cfg.PublicNamespace, a.cfg.Domain)
	base := a.cfg.PublicNamespace + "." + a.cfg.Domain
	req := ClaimRequest{Host: host, Kind: ClaimNamespace}
	return Decision{Host: host, Mode: ClaimNamespace, Ephemeral: true, Base: base, claimReq: req}, nil
}

// namespaceLabels returns the set of namespace labels granted by active tokens
// (the immediate child of the domain in each non-root allow-pattern base). They
// are reserved against root-tier claims so a *.<domain> token cannot grab a name
// that belongs to someone's namespace.
func (a *Authorizer) namespaceLabels(ctx context.Context) (ReservedSet, error) {
	bases, err := a.store.TokenBases(ctx)
	if err != nil {
		return nil, err
	}
	set := make(ReservedSet)
	for _, base := range bases {
		if base == a.cfg.Domain {
			continue
		}
		if label, ok := ImmediateChildLabel(base, a.cfg.Domain); ok {
			set[label] = struct{}{}
		}
	}
	return set, nil
}

// resolvePublicLabel resolves the single public label for a claim. Every public
// host is one label under its base, so multi-label route names are rejected. A
// Random attempt overrides any provided name.
func resolvePublicLabel(attempt ClaimAttempt) (string, *AuthzError) {
	if attempt.Random {
		return randomLabel(), nil
	}
	if attempt.Route == "" {
		return "", &AuthzError{Status: http.StatusBadRequest, Reason: "no route name"}
	}
	parsed, err := route.Parse(attempt.Route)
	if err != nil {
		return "", &AuthzError{Status: http.StatusBadRequest, Reason: "invalid route name: " + err.Error()}
	}
	if len(parsed.Labels) != 1 {
		return "", &AuthzError{
			Status: http.StatusBadRequest,
			Reason: fmt.Sprintf(
				"public route names must be a single label; %q can't be a public host, try %q instead",
				parsed.String(), strings.Join(parsed.Labels, "-")),
		}
	}
	return parsed.String(), nil
}

// randomLabel returns a server-assigned label, letter-leading so it is a valid
// DNS label regardless of the random bytes.
func randomLabel() string {
	buf := make([]byte, 5)
	_, _ = rand.Read(buf)
	return "r" + hex.EncodeToString(buf)
}
