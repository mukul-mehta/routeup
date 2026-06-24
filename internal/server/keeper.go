package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// tunnelKeeper implements tunnel.ClaimKeeper over the authorizer and store: it
// authorizes a route for a token and actually holds the claim for the session's
// lifetime.
type tunnelKeeper struct {
	authz           *Authorizer
	store           *Store
	ensureNamespace func(ctx context.Context, base string)
}

// Hold authorizes spec for token and holds the resulting claim.
func (k *tunnelKeeper) Hold(ctx context.Context, token string, spec tunnel.ClaimSpec) (string, error) {
	decision, err := k.authz.Authorize(ctx, ClaimAttempt{
		TokenSecret: token,
		Route:       spec.Route,
		Random:      spec.Random,
	})
	if err != nil {
		var ae *AuthzError
		if errors.As(err, &ae) {
			return "", &codedError{msg: ae.Reason, code: ae.Status}
		}
		return "", err
	}

	if _, err := k.store.Claim(ctx, decision.ClaimRequest()); err != nil {
		if errors.Is(err, ErrClaimConflict) {
			return "", &codedError{msg: "route already claimed", code: http.StatusConflict}
		}
		return "", err
	}

	// Ensure a wildcard certificate exists for this namespace (lazy issuance
	// for token namespaces; a no-op once already managed).
	if k.ensureNamespace != nil {
		k.ensureNamespace(ctx, decision.Base)
	}
	return decision.Host, nil
}

// Release ends the hold for host (grace window for token claims).
func (k *tunnelKeeper) Release(host string) {
	_ = k.store.Release(context.Background(), host)
}

// codedError carries an HTTP-style status code back through the tunnel to the
// client so it can show a precise rejection reason.
type codedError struct {
	msg  string
	code int
}

func (e *codedError) Error() string   { return e.msg }
func (e *codedError) StatusCode() int { return e.code }
