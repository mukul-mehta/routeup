package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/proxy"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// tunnelSession is one live public tunnel plus what's needed to stop it.
// ownerPID is the CLI process that requested it; if that process exits without
// unexposing, the manager reaps the session and cancels its tunnel context.
type tunnelSession struct {
	host     string
	route    string
	paths    []string
	ownerPID int
	state    ipc.ExposureState
	cancel   context.CancelFunc
	done     bool
	err      error
	claim    exposureClaim
}

// tunnelManager owns the agent's live public tunnels. The CLI asks it to expose
// a local target set; it starts the tunnel and keeps it running after the IPC
// request returns, until the CLI unexposes or its process dies. Entries are
// keyed by the public host the server granted.
type tunnelManager struct {
	parent context.Context
	logger *slog.Logger
	logs   *logs.Store

	mu                   sync.RWMutex
	activeTunnelSessions map[string]*tunnelSession
	claimSessions        map[exposureClaim]*tunnelSession
	wg                   sync.WaitGroup
}

func newTunnelManager(parent context.Context, logStore *logs.Store, logger *slog.Logger) *tunnelManager {
	return &tunnelManager{
		parent:               parent,
		logger:               logger,
		logs:                 logStore,
		activeTunnelSessions: make(map[string]*tunnelSession),
		claimSessions:        make(map[exposureClaim]*tunnelSession),
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
	handler := proxy.NewTargets(targets, paths, req.Route, req.CaptureRequest, req.CaptureResponse, req.RedactHeaders, m.logs, m.logger)

	tunnelCtx, cancel := context.WithCancel(m.parent)
	grantedCh := make(chan string, 1)
	errCh := make(chan error, 1)
	session := &tunnelSession{
		route:    req.Route,
		paths:    append([]string(nil), paths...),
		ownerPID: req.OwnerPID,
		state:    ipc.ExposureReconnecting,
		cancel:   cancel,
		claim:    exposureClaim{server: req.Server, name: req.Name, tokenHash: sha256.Sum256([]byte(req.Token))},
	}
	if err := m.reserve(session); err != nil {
		cancel()
		return "", err
	}

	client := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL: req.Server,
		Token:     req.Token,
		Spec:      tunnel.ClaimSpec{Route: req.Name},
		Handler:   handler,
		Logger:    m.logger,
		OnGranted: func(host string) {
			if !m.setSessionState(session, host, ipc.ExposureConnected) {
				return
			}
			select {
			case grantedCh <- host:
			default:
			}
		},
		OnDisconnected: func(error) {
			_ = m.setSessionState(session, "", ipc.ExposureReconnecting)
		},
	})
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		err := client.Run(tunnelCtx)
		m.finish(session, err)
		errCh <- err
	}()

	select {
	case host := <-grantedCh:
		if host == "" {
			cancel()
			return "", errors.New("server granted no host")
		}
		if err := m.store(host, session); err != nil {
			cancel()
			return "", err
		}
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

// Unexpose tears down the tunnel only when its host, route, and owner match.
func (m *tunnelManager) Unexpose(req ipc.UnexposeRequest) bool {
	m.mu.Lock()
	s, ok := m.activeTunnelSessions[req.Host]
	if ok && s.route == req.Route && s.ownerPID == req.OwnerPID {
		delete(m.activeTunnelSessions, req.Host)
	} else {
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	s.cancel()
	m.logger.Info("tunnel released", "host", req.Host)
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

// publicExposures maps managed tunnels by owner and local route so route status
// cannot attach one process's exposure to the wrong claim.
func (m *tunnelManager) publicExposures() map[exposureKey]publicExposure {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[exposureKey]publicExposure, len(m.activeTunnelSessions))
	for _, s := range m.activeTunnelSessions {
		out[exposureKey{ownerPID: s.ownerPID, route: s.route}] = publicExposure{
			host: s.host, paths: append([]string(nil), s.paths...), state: s.state,
		}
	}
	return out
}

func (m *tunnelManager) statuses() []ipc.ExposureStatus {
	m.mu.RLock()
	out := make([]ipc.ExposureStatus, 0, len(m.activeTunnelSessions))
	for _, s := range m.activeTunnelSessions {
		out = append(out, ipc.ExposureStatus{
			Route: s.route, Host: s.host, Paths: append([]string(nil), s.paths...),
			OwnerPID: s.ownerPID, State: s.state,
		})
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func (m *tunnelManager) store(host string, s *tunnelSession) error {
	m.mu.Lock()
	if s.done {
		err := s.err
		m.mu.Unlock()
		if err == nil {
			err = errors.New("tunnel closed after establishing")
		}
		return err
	}
	if old := m.activeTunnelSessions[host]; old != nil && old != s {
		m.mu.Unlock()
		return fmt.Errorf("public host %q is already managed by pid %d", host, old.ownerPID)
	}
	s.host = host
	m.activeTunnelSessions[host] = s
	m.mu.Unlock()
	return nil
}

func (m *tunnelManager) reserve(s *tunnelSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.claimSessions[s.claim]; existing != nil {
		return fmt.Errorf("public route %q on %s is already managed by pid %d", s.claim.name, s.claim.server, existing.ownerPID)
	}
	m.claimSessions[s.claim] = s
	return nil
}

func (m *tunnelManager) setSessionState(s *tunnelSession, host string, state ipc.ExposureState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.done {
		return false
	}
	if host != "" {
		if s.host != "" && s.host != host {
			m.logger.Error("public host changed while reconnecting", "from", s.host, "to", host)
			s.cancel()
			return false
		}
		s.host = host
	}
	s.state = state
	return true
}

func (m *tunnelManager) finish(s *tunnelSession, err error) {
	m.mu.Lock()
	s.done = true
	s.err = err
	if s.host != "" && m.activeTunnelSessions[s.host] == s {
		delete(m.activeTunnelSessions, s.host)
	}
	if m.claimSessions[s.claim] == s {
		delete(m.claimSessions, s.claim)
	}
	m.mu.Unlock()
	s.cancel()
	if s.host != "" && err != nil && !errors.Is(err, context.Canceled) {
		m.logger.Warn("tunnel stopped", "host", s.host, "err", err)
	}
}

func (m *tunnelManager) Wait() {
	m.wg.Wait()
}

type publicExposure struct {
	host  string
	paths []string
	state ipc.ExposureState
}

type exposureKey struct {
	ownerPID int
	route    string
}

type exposureClaim struct {
	server    string
	name      string
	tokenHash [32]byte
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
