// Package agent implements the local routeup daemon (the server side of the
// CLI<->agent IPC): it holds the in-memory route registry, serves the
// per-user Unix-socket control API, and reverse-proxies HTTPS by Host header
// to local targets. The CLI stub that talks to it lives in internal/agentctl;
// the shared wire types live in internal/ipc.
package agent

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/proxy"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/state"
)

// Options for New. Zero values get sane defaults.
type Options struct {
	SocketPath string
	TLSAddr    string
	CAPath     string
	CAKey      string
	Version    string
	Logger     *slog.Logger
}

// Agent is the local routeup daemon. New binds nothing; Run does the work.
type Agent struct {
	reg           *Registry
	tunnels       *tunnelManager
	sockPath      string
	tlsAddr       string
	tlsListenAddr string
	caPath        string
	caKeyPath     string
	version       string
	bootID        string
	logger        *slog.Logger
	startedAt     time.Time
	execPath      string
	execModTime   time.Time
	shutdownOnce  sync.Once
	shutdownCh    chan struct{}
}

// New validates options and returns an Agent ready to Run.
func New(opts Options) (*Agent, error) {
	if opts.SocketPath == "" {
		return nil, errors.New("agent: SocketPath is required")
	}
	if opts.TLSAddr == "" {
		opts.TLSAddr = ipc.DefaultTLSAddr
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	execPath, execModTime := execInfo()

	return &Agent{
		reg:       NewRegistry(),
		sockPath:  opts.SocketPath,
		tlsAddr:   opts.TLSAddr,
		caPath:    opts.CAPath,
		caKeyPath: opts.CAKey,
		version:   opts.Version,
		// bootID changes every process start so the CLI can detect a restart.
		bootID:      rand.Text(),
		logger:      opts.Logger,
		execPath:    execPath,
		execModTime: execModTime,
		shutdownCh:  make(chan struct{}),
	}, nil
}

// Run starts the UDS API and the TLS reverse proxy, plus the periodic reap
// loop. It returns when ctx is cancelled, a shutdown is requested via the
// API, or any of the servers fails fatally. On return, the socket file is
// removed and both servers are stopped.
func (a *Agent) Run(ctx context.Context) error {
	certPath, keyPath, err := a.resolveCAPaths()
	if err != nil {
		return err
	}
	ca, err := certs.EnsureCA(certPath, keyPath)
	if err != nil {
		return err
	}
	a.logger.Info("loaded local CA", "path", certPath, "expires", ca.Cert.NotAfter)

	if err := state.EnsureParentDir(a.sockPath); err != nil {
		return err
	}
	if err := clearStaleSocket(a.sockPath); err != nil {
		return err
	}

	udsListener, err := net.Listen("unix", a.sockPath)
	if err != nil {
		return fmt.Errorf("bind socket %s: %w", a.sockPath, err)
	}
	if err := os.Chmod(a.sockPath, 0o600); err != nil {
		_ = udsListener.Close()
		_ = os.Remove(a.sockPath)
		return fmt.Errorf("chmod socket: %w", err)
	}

	issuer := certs.NewIssuer(ca, "."+route.LocalSuffix)
	tlsCfg := &tls.Config{
		GetCertificate: issuer.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
	rawTLS, err := net.Listen("tcp", a.tlsAddr)
	if err != nil {
		_ = udsListener.Close()
		_ = os.Remove(a.sockPath)
		return fmt.Errorf("bind tls %s: %w", a.tlsAddr, err)
	}
	tlsListener := tls.NewListener(rawTLS, tlsCfg)
	a.tlsListenAddr = rawTLS.Addr().String()

	a.writePIDFile()
	defer a.removePIDFile()

	a.startedAt = time.Now()
	a.tunnels = newTunnelManager(ctx, a.logger)
	a.logger.Info("agent started",
		"socket", a.sockPath,
		"tls_addr", a.tlsListenAddr,
		"version", a.version)

	apiSrv := &http.Server{
		Handler:           a.apiHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}
	proxySrv := &http.Server{
		Handler:           proxy.New(a.reg, a.logger),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 2)
	go func() {
		if err := apiSrv.Serve(udsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("api server: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		if err := proxySrv.Serve(tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("tls server: %w", err)
			return
		}
		errCh <- nil
	}()

	reapCtx, reapCancel := context.WithCancel(ctx)
	reapDone := make(chan struct{})
	go func() {
		defer close(reapDone)
		a.runReap(reapCtx)
	}()

	var fatal error
	select {
	case <-ctx.Done():
		a.logger.Info("shutdown: context cancelled")
	case <-a.shutdownCh:
		a.logger.Info("shutdown: requested via api")
	case err := <-errCh:
		if err != nil {
			fatal = err
			a.logger.Error("listener failed", "err", err)
		}
	}

	reapCancel()
	<-reapDone

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = apiSrv.Shutdown(shutdownCtx)
	_ = proxySrv.Shutdown(shutdownCtx)

	if err := os.Remove(a.sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.logger.Warn("remove socket", "err", err)
	}

	a.logger.Info("agent stopped")
	return fatal
}

func (a *Agent) triggerShutdown() {
	a.shutdownOnce.Do(func() { close(a.shutdownCh) })
}

func (a *Agent) writePIDFile() {
	path, err := state.AgentPIDPath()
	if err != nil {
		a.logger.Warn("resolve pid file", "err", err)
		return
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		a.logger.Warn("write pid file", "err", err)
	}
}

func (a *Agent) removePIDFile() {
	path, err := state.AgentPIDPath()
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.logger.Warn("remove pid file", "err", err)
	}
}

func (a *Agent) runReap(ctx context.Context) {
	const interval = 10 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n := a.reg.Reap(); n > 0 {
				a.logger.Info("reaped stale claims", "count", n)
			}
			if a.tunnels != nil {
				if n := a.tunnels.ReapDeadOwners(); n > 0 {
					a.logger.Info("reaped orphaned tunnels", "count", n)
				}
			}
		}
	}
}

func clearStaleSocket(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("another agent is already listening on %s", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	return nil
}

func execInfo() (string, time.Time) {
	exe, err := os.Executable()
	if err != nil {
		return "", time.Time{}
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return exe, time.Time{}
	}
	return exe, fi.ModTime()
}

func (a *Agent) resolveCAPaths() (string, string, error) {
	certPath := a.caPath
	if certPath == "" {
		p, err := state.CACertPath()
		if err != nil {
			return "", "", err
		}
		certPath = p
	}
	keyPath := a.caKeyPath
	if keyPath == "" {
		p, err := state.CAKeyPath()
		if err != nil {
			return "", "", err
		}
		keyPath = p
	}
	return certPath, keyPath, nil
}
