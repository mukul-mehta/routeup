package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/proxy"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// tunnelSession is one live public tunnel plus what's needed to stop it.
// ownerPID is the CLI process that requested it; if that process exits without
// unexposing, the manager reaps the session and cancels its tunnel context.
type tunnelSession struct {
	host     string
	paths    []string
	ownerPID int
	cancel   context.CancelFunc
}

// tunnelManager owns the agent's live public tunnels. The CLI asks it to expose
// a local port; it starts the tunnel and keeps it running after the IPC request
// returns, until the CLI unexposes or its process dies. Entries are keyed by the
// public host the server granted.
type tunnelManager struct {
	parent context.Context
	logger *slog.Logger

	mu                   sync.Mutex
	activeTunnelSessions map[string]*tunnelSession
}

func newTunnelManager(parent context.Context, logger *slog.Logger) *tunnelManager {
	return &tunnelManager{
		parent:               parent,
		logger:               logger,
		activeTunnelSessions: make(map[string]*tunnelSession),
	}
}

// Expose starts a tunnel for req and blocks only until the claim handshake
// resolves — it returns once the server grants a host, the tunnel fails, or the
// IPC request is cancelled. It does NOT block for the tunnel's lifetime.
//
// The distinction matters: the tunnel runs under m.parent (the agent's lifetime
// context), not reqCtx (this IPC request's context). So when reqCtx ends — the
// CLI got its host back and the `expose` call returned — the tunnel keeps
// serving public requests. It only stops on Unexpose, owner death, or agent
// shutdown.
func (m *tunnelManager) Expose(reqCtx context.Context, req ipc.ExposeRequest) (string, error) {
	targets, err := normalizeExposeTargets(req)
	if err != nil {
		return "", err
	}
	paths, err := route.NormalizePathPatterns(req.Paths)
	if err != nil {
		return "", err
	}
	handler := proxy.NewTargets(targets, paths, m.logger)

	tunnelCtx, cancel := context.WithCancel(m.parent)
	grantedCh := make(chan string, 1)
	errCh := make(chan error, 1)

	client := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: req.Server,
		Token:     req.Token,
		Spec:      tunnel.ClaimSpec{Route: req.Name},
		Handler:   handler,
		Logger:    m.logger,
		OnGranted: func(host string) {
			select {
			case grantedCh <- host:
			default:
			}
		},
	})
	go func() { errCh <- client.Run(tunnelCtx) }()

	// Wait for whichever of the three outcomes happens first. The tunnel
	// goroutine above keeps running regardless of which branch we take here
	// (unless we cancel it).
	//
	// The 3 outcomes are:
	// 1. The server accepted claim and returned the public host.
	// 2. There was an error and the tunnel died before granting a host
	// 3. THe CLI received a Ctrl+C and cancelled the IPC request
	select {
	case host := <-grantedCh:
		if host == "" {
			cancel()
			return "", errors.New("server granted no host")
		}
		m.store(host, &tunnelSession{host: host, paths: paths, ownerPID: req.OwnerPID, cancel: cancel})
		m.logger.Info("tunnel established", "host", host, "targets", len(targets))
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

// Unexpose tears down the tunnel for host. It returns true if one existed.
func (m *tunnelManager) Unexpose(host string) bool {
	m.mu.Lock()
	s, ok := m.activeTunnelSessions[host]
	if ok {
		delete(m.activeTunnelSessions, host)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	s.cancel()
	m.logger.Info("tunnel released", "host", host)
	return true
}

// ReapDeadOwners tears down tunnels whose owning CLI process has exited.
func (m *tunnelManager) ReapDeadOwners() int {
	m.mu.Lock()
	var dead []*tunnelSession
	for host, session := range m.activeTunnelSessions {
		if !defaultPIDAlive(session.ownerPID) {
			dead = append(dead, session)
			delete(m.activeTunnelSessions, host)
		}
	}
	m.mu.Unlock()
	for _, s := range dead {
		s.cancel()
	}
	return len(dead)
}

// publicExposures maps each live tunnel's owner PID to its granted public host,
// so the routes listing can show which local routes are currently exposed. A
// serve process owns at most one tunnel, so one PID maps to one host.
func (m *tunnelManager) publicExposures() map[int]publicExposure {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int]publicExposure, len(m.activeTunnelSessions))
	for _, s := range m.activeTunnelSessions {
		out[s.ownerPID] = publicExposure{host: s.host, paths: append([]string(nil), s.paths...)}
	}
	return out
}

func (m *tunnelManager) store(host string, s *tunnelSession) {
	m.mu.Lock()
	if old, ok := m.activeTunnelSessions[host]; ok {
		old.cancel()
	}
	m.activeTunnelSessions[host] = s
	m.mu.Unlock()
}

type publicExposure struct {
	host  string
	paths []string
}

func normalizeExposeTargets(req ipc.ExposeRequest) ([]route.Target, error) {
	targets := req.Targets
	if len(targets) == 0 && req.Port != 0 {
		targets = []route.Target{{Path: "/", Port: req.Port}}
	}
	normalized, err := route.NormalizeTargets(targets)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, errors.New("at least one target is required")
	}
	return normalized, nil
}
