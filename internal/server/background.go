package server

import (
	"context"
	"time"
)

// background.go holds the server's long-running background loops, started as
// goroutines by Run and stopped when its context is cancelled:
//
//   - runReap         deletes token holds whose grace window has expired.
//   - runCertPrewarm  keeps a wildcard certificate managed for every active
//     token namespace, so a route is reachable the instant it's exposed.
//
// Each loop owns nothing: it reads through the Server's store and cert manager,
// and returns promptly when ctx is done so Run's shutdown can join it.

const (
	reapInterval        = 10 * time.Second
	certPrewarmInterval = 60 * time.Second
)

// runReap deletes released token holds whose grace window has elapsed, on a
// fixed interval, until ctx is cancelled.
func (s *Server) runReap(ctx context.Context) {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.store.ReapExpiredHolds(ctx)
			if err != nil {
				s.logger.Warn("reap holds", "err", err)
				continue
			}
			if n > 0 {
				s.logger.Info("reaped expired holds", "count", n)
			}
		}
	}
}

// runCertPrewarm pre-issues namespace wildcards now and on a reconcile interval,
// picking up newly-minted token namespaces without a restart.
func (s *Server) runCertPrewarm(ctx context.Context) {
	s.prewarmNamespaceCerts(ctx)

	t := time.NewTicker(certPrewarmInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.prewarmNamespaceCerts(ctx)
		}
	}
}

// prewarmNamespaceCerts asks the cert manager to manage a wildcard for every
// active token namespace. The root wildcard is already managed at startup, and
// EnsureNamespace is idempotent (a no-op once a namespace is managed, and a
// no-op entirely for static certs).
func (s *Server) prewarmNamespaceCerts(ctx context.Context) {
	if s.cm == nil {
		return
	}
	bases, err := s.store.TokenBases(ctx)
	if err != nil {
		s.logger.Warn("cert prewarm: list token namespaces", "err", err)
		return
	}
	for _, base := range bases {
		if base == s.cfg.Domain {
			continue // root wildcard is managed at startup
		}
		s.cm.EnsureNamespace(ctx, base)
	}
}
