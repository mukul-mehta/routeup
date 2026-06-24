package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// routeBroker implements tunnel.RouteBroker. It is the bridge between the tunnel
// (which knows nothing about tokens, storage, or TLS) and the server's policy
// and persistence: when an agent claims a route, Hold authorizes it against the
// token, persists the hold, and ensures a wildcard certificate for its
// namespace. Release ends the hold when the agent disconnects.
type routeBroker struct {
	authorizer      *Authorizer
	store           *Store
	ensureNamespace func(ctx context.Context, base string)
}

// Hold authorizes spec for token, persists the hold, and ensures a cert for its
// namespace. It returns the resolved public host.
func (k *routeBroker) Hold(ctx context.Context, token string, spec tunnel.ClaimSpec) (string, error) {
	decision, err := k.authorizer.Authorize(ctx, ClaimAttempt{
		TokenSecret: token,
		Route:       spec.Route,
	})
	if err != nil {
		var ae *AuthzError
		if errors.As(err, &ae) {
			return "", &codedError{msg: ae.Reason, code: ae.Status}
		}
		return "", err
	}

	if _, err := k.store.HoldRoute(ctx, decision.HoldRequest()); err != nil {
		if errors.Is(err, ErrRouteConflict) {
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

// Release ends the hold for host (grace window for token holds).
func (k *routeBroker) Release(host string) {
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
