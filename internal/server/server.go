package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// Server is the routeup public server: it serves the health endpoint, the
// tunnel endpoint, and public ingress, backed by the SQLite store.
type Server struct {
	cfg    ServerConfig
	logger *slog.Logger

	store *Store
	authz *Authorizer
	hub   *tunnel.Hub
	cm    certManager
}

// New validates cfg and returns a Server ready to Run. The store is opened by
// Run so its lifetime matches the serving context.
func New(cfg ServerConfig, logger *slog.Logger) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{cfg: cfg, logger: logger}, nil
}

// NewWithStore builds a server around an already-open store, for embedding and
// tests. It does not own the store's lifecycle (the caller closes it).
func NewWithStore(cfg ServerConfig, store *Store, logger *slog.Logger) (*Server, error) {
	s, err := New(cfg, logger)
	if err != nil {
		return nil, err
	}
	s.attach(store)
	return s, nil
}

// Handler returns the server's HTTP handler. The server must have a store
// attached (via Run or NewWithStore).
func (s *Server) Handler() http.Handler { return s.handler() }

// attach wires the store, authorizer, and tunnel hub onto the server.
func (s *Server) attach(store *Store) {
	s.store = store
	s.authz = NewAuthorizer(s.cfg, store)
	keeper := &tunnelKeeper{
		authz:           s.authz,
		store:           store,
		ensureNamespace: s.ensureNamespaceCert,
	}
	s.hub = tunnel.NewHub(keeper, s.logger)
}

// ensureNamespaceCert asks the cert manager to manage a wildcard for base. It
// is safe before Run sets a cert manager (tests serve Handler() directly).
func (s *Server) ensureNamespaceCert(ctx context.Context, base string) {
	if s.cm != nil {
		s.cm.EnsureNamespace(ctx, base)
	}
}

// Run opens the store, purges stale ephemeral claims, starts the grace reaper
// and the HTTP listener, and serves until ctx is cancelled or the listener
// fails fatally.
func (s *Server) Run(ctx context.Context) error {
	store, err := OpenStore(ctx, s.cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	s.attach(store)

	if n, err := store.PurgeEphemeralClaims(ctx); err != nil {
		return err
	} else if n > 0 {
		s.logger.Info("purged ephemeral claims at startup", "count", n)
	}

	reapCtx, cancelReap := context.WithCancel(ctx)
	defer cancelReap()
	reapDone := make(chan struct{})
	go func() {
		defer close(reapDone)
		s.runReap(reapCtx)
	}()

	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	tlsCfg, err := s.buildCertManager(ctx)
	if err != nil {
		return err
	}
	s.cm = tlsCfg
	srv.TLSConfig = tlsCfg.TLSConfig()

	// Pre-warm a wildcard certificate for every token namespace, so a route is
	// reachable the instant it's exposed instead of waiting on first-claim
	// issuance. Runs now and on a short reconcile to pick up newly-minted tokens.
	prewarmDone := make(chan struct{})
	go func() {
		defer close(prewarmDone)
		s.runCertPrewarm(reapCtx)
	}()

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("server listening",
			"addr", s.cfg.Listen, "domain", s.cfg.Domain,
			"public_namespace", s.cfg.PublicNamespace, "tls_mode", s.cfg.TLSMode)

		// Certificates come from srv.TLSConfig (static, internal, or certmagic's
		// GetCertificate), so the file arguments are empty.
		serveErr := srv.ListenAndServeTLS("", "")
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	var fatal error
	select {
	case <-ctx.Done():
		s.logger.Info("shutdown: context cancelled")
	case err := <-errCh:
		fatal = err
		if err != nil {
			s.logger.Error("listener failed", "err", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	cancelReap()
	<-reapDone
	<-prewarmDone
	return fatal
}

// runCertPrewarm pre-issues namespace wildcards now and on a reconcile interval,
// picking up newly-minted token namespaces without a restart.
func (s *Server) runCertPrewarm(ctx context.Context) {
	s.prewarmNamespaceCerts(ctx)

	const interval = 60 * time.Second
	t := time.NewTicker(interval)
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

func (s *Server) runReap(ctx context.Context) {
	const interval = 10 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.store.ReapExpiredClaims(ctx)
			if err != nil {
				s.logger.Warn("reap claims", "err", err)
				continue
			}
			if n > 0 {
				s.logger.Info("reaped expired claims", "count", n)
			}
		}
	}
}
