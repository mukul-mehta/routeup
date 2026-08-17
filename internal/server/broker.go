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
	metrics         *serverMetrics
	claimLimiter    *multiLimiter
	anonLimiter     *multiLimiter
}

// Hold authorizes spec for token, persists the hold, and ensures a cert for its
// namespace. It returns the resolved public host.
func (k *routeBroker) Hold(ctx context.Context, token string, spec tunnel.ClaimSpec) (tunnel.RouteLease, error) {
	var (
		decision Decision
		err      error
	)

	if token == "" {
		if !k.anonLimiter.allow("anon") {
			k.metrics.claimRateLimited()
			return tunnel.RouteLease{}, &codedError{msg: "rate limit exceeded", code: http.StatusTooManyRequests}
		}
		decision, err = k.authorizer.Authorize(ctx, ClaimAttempt{Route: spec.Route})
	} else {
		tok, authErr := k.authorizer.authenticateToken(ctx, token)
		if authErr != nil {
			// Invalid credentials share one bounded bucket. Never retain the
			// presented secret as limiter state.
			if !k.claimLimiter.allow("invalid") {
				k.metrics.claimRateLimited()
				return tunnel.RouteLease{}, &codedError{msg: "rate limit exceeded", code: http.StatusTooManyRequests}
			}
			err = authErr
		} else if !k.claimLimiter.allow("token:" + tok.ID) {
			k.metrics.claimRateLimited()
			return tunnel.RouteLease{}, &codedError{msg: "rate limit exceeded", code: http.StatusTooManyRequests}
		} else {
			decision, err = k.authorizer.authorizeVerifiedToken(ctx, ClaimAttempt{Route: spec.Route}, tok)
		}
	}

	if err != nil {
		k.metrics.claimRejected()
		var ae *AuthzError
		if errors.As(err, &ae) {
			return tunnel.RouteLease{}, &codedError{msg: ae.Reason, code: ae.Status}
		}
		return tunnel.RouteLease{}, err
	}

	hold, err := k.store.HoldRoute(ctx, decision.HoldRequest())
	if err != nil {
		k.metrics.claimRejected()
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
	k.metrics.claimAccepted()
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
