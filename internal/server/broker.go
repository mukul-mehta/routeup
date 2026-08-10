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
func (k *routeBroker) Hold(ctx context.Context, token string, spec tunnel.ClaimSpec) (tunnel.RouteLease, error) {
	decision, err := k.authorizer.Authorize(ctx, ClaimAttempt{
		TokenSecret: token,
		Route:       spec.Route,
	})
	if err != nil {
		var ae *AuthzError
		if errors.As(err, &ae) {
			return tunnel.RouteLease{}, &codedError{msg: ae.Reason, code: ae.Status}
		}
		return tunnel.RouteLease{}, err
	}

	hold, err := k.store.HoldRoute(ctx, decision.HoldRequest())
	if err != nil {
		if errors.Is(err, ErrRouteConflict) {
			return tunnel.RouteLease{}, &codedError{msg: "route already claimed", code: http.StatusConflict}
		}
		return tunnel.RouteLease{}, err
	}

	// Ensure a wildcard certificate exists for this namespace (lazy issuance
	// for token namespaces; a no-op once already managed).
	if k.ensureNamespace != nil {
		k.ensureNamespace(ctx, decision.Base)
	}
	return tunnel.RouteLease{Host: decision.Host, Generation: hold.HeldAt.UnixNano()}, nil
}

// Release ends only the hold generation represented by lease.
func (k *routeBroker) Release(lease tunnel.RouteLease) {
	_ = k.store.ReleaseGeneration(context.Background(), lease.Host, lease.Generation)
}

// codedError carries an HTTP-style status code back through the tunnel to the
// client so it can show a precise rejection reason.
type codedError struct {
	msg  string
	code int
}

func (e *codedError) Error() string   { return e.msg }
func (e *codedError) StatusCode() int { return e.code }
