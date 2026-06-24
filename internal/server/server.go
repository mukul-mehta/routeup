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

	store      *Store
	authorizer *Authorizer
	tunnels    *tunnel.TunnelRegistry
	cm         certManager
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

// attach wires the store, authorizer, route broker, and tunnel registry onto
// the server. It is the shared setup path for Run; tests reach it through a
// helper to get a store-backed server without binding a listener.
func (s *Server) attach(store *Store) {
	s.store = store
	s.authorizer = NewAuthorizer(s.cfg, store)
	broker := &routeBroker{
		authorizer:      s.authorizer,
		store:           store,
		ensureNamespace: s.ensureNamespaceCert,
	}
	s.tunnels = tunnel.NewTunnelRegistry(broker, s.logger)
}

// ensureNamespaceCert asks the cert manager to manage a wildcard for base. It
// is safe before Run sets a cert manager (tests serve handler() directly).
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

	if n, err := store.PurgeEphemeralHolds(ctx); err != nil {
		return err
	} else if n > 0 {
		s.logger.Info("purged ephemeral holds at startup", "count", n)
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
