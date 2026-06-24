package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// exposer is the agent's public-tunnel manager. The CLI asks it to expose a
// local port, and it keeps the resulting tunnel alive after the IPC request
// returns. Entries are keyed by the public host the server granted.
type exposer struct {
	parent context.Context
	logger *slog.Logger

	mu     sync.Mutex
	active map[string]*exposure
}

// exposure is one live public tunnel plus the information needed to stop it.
// ownerPID is the CLI process that requested it; if that process exits without
// unexposing, the agent reaps this exposure and cancels the tunnel context.
type exposure struct {
	host     string
	ownerPID int
	cancel   context.CancelFunc
}

func newExposer(parent context.Context, logger *slog.Logger) *exposer {
	return &exposer{
		parent: parent,
		logger: logger,
		active: make(map[string]*exposure),
	}
}

// Expose starts a tunnel for req and blocks until the server grants a public
// host, a permanent error occurs, or reqCtx is cancelled. The tunnel itself
// runs under the exposer's parent context, so it outlives the request.
func (e *exposer) Expose(reqCtx context.Context, req ipc.ExposeRequest) (string, error) {
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", strconv.Itoa(req.Port))}
	handler := newTunnelProxy(target, e.logger)

	cctx, cancel := context.WithCancel(e.parent)
	grantedCh := make(chan []string, 1)
	errCh := make(chan error, 1)

	client := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: req.Server,
		Token:     req.Token,
		Specs:     []tunnel.ClaimSpec{{Route: req.Name, Random: req.Random}},
		Handler:   handler,
		Logger:    e.logger,
		OnGranted: func(hosts []string) {
			select {
			case grantedCh <- hosts:
			default:
			}
		},
	})
	go func() { errCh <- client.Run(cctx) }()

	select {
	case hosts := <-grantedCh:
		if len(hosts) == 0 {
			cancel()
			return "", errors.New("server granted no host")
		}
		host := hosts[0]
		e.store(host, &exposure{host: host, ownerPID: req.OwnerPID, cancel: cancel})
		e.logger.Info("exposure established", "host", host, "port", req.Port)
		return host, nil

	case err := <-errCh:
		cancel()
		if err == nil {
			err = errors.New("tunnel closed before establishing")
		}
		return "", err

	case <-reqCtx.Done():
		cancel()
		return "", reqCtx.Err()
	}
}

// Unexpose tears down the exposure for host. It returns true if one existed.
func (e *exposer) Unexpose(host string) bool {
	e.mu.Lock()
	ex, ok := e.active[host]
	if ok {
		delete(e.active, host)
	}
	e.mu.Unlock()
	if !ok {
		return false
	}
	ex.cancel()
	e.logger.Info("exposure released", "host", host)
	return true
}

// ReapDeadOwners tears down exposures whose owning CLI process has exited.
func (e *exposer) ReapDeadOwners() int {
	e.mu.Lock()
	var dead []*exposure
	for host, ex := range e.active {
		if !defaultPIDAlive(ex.ownerPID) {
			dead = append(dead, ex)
			delete(e.active, host)
		}
	}
	e.mu.Unlock()
	for _, ex := range dead {
		ex.cancel()
	}
	return len(dead)
}

func (e *exposer) store(host string, ex *exposure) {
	e.mu.Lock()
	if old, ok := e.active[host]; ok {
		old.cancel()
	}
	e.active[host] = ex
	e.mu.Unlock()
}

// newTunnelProxy builds a reverse proxy to the local target, preserving the
// inbound (public) Host header so the local app sees its real hostname.
func newTunnelProxy(target *url.URL, logger *slog.Logger) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Warn("tunnel upstream error", "target", target.Host, "err", err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, fmt.Sprintf("routeup: local upstream %s unavailable\n", target.Host))
		},
	}
}
