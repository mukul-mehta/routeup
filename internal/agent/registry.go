package agent

import (
	"errors"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
)

// Registry holds the agent's active route claims in memory. Mutating methods
// take the write lock; LookupTargets and List take the read lock.
//
// A claim whose owning PID has exited is an orphan. Register replaces an orphan
// in place, and Reap drops orphans on a timer. There is no persistence: the
// registry starts empty on every agent run.
type Registry struct {
	mu     sync.RWMutex
	claims map[string]ipc.Claim
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{claims: make(map[string]ipc.Claim)}
}

// Register inserts c and stamps RegisteredAt (any incoming value is ignored).
//
// If the name is already held by the same PID, it is updated in place, since
// re-registering a route you own is not a conflict (this is what lets the
// reconcile loop re-assert freely). If held by a different but dead PID, it is
// replaced. If held by a different, live PID, it returns *ipc.ConflictError and
// leaves the registry unchanged.
func (r *Registry) Register(c ipc.Claim) (ipc.Claim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	normalized, err := normalizeClaim(c)
	if err != nil {
		return ipc.Claim{}, err
	}

	if existing, ok := r.claims[normalized.Name]; ok {
		differentOwner := existing.OwnerPID != normalized.OwnerPID
		if differentOwner && defaultPIDAlive(existing.OwnerPID) {
			return ipc.Claim{}, &ipc.ConflictError{Name: normalized.Name, Existing: existing}
		}
	}

	normalized.RegisteredAt = time.Now()
	r.claims[normalized.Name] = normalized
	return normalized, nil
}

// Unregister removes the claim for name. It returns true if a claim was
// removed. Missing claims are not an error: DELETE is idempotent.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.claims[name]; !ok {
		return false
	}
	delete(r.claims, name)
	return true
}

// List returns a snapshot of active claims, sorted by name for stable output.
func (r *Registry) List() []ipc.Claim {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ipc.Claim, 0, len(r.claims))
	for _, c := range r.claims {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupTargets returns the configured targets for name, if registered. Used by
// the reverse proxy to translate Host + path -> upstream.
func (r *Registry) LookupTargets(name string) ([]route.Target, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.claims[name]
	if !ok {
		return nil, false
	}
	targets := make([]route.Target, len(c.Targets))
	copy(targets, c.Targets)
	return targets, true
}

// Lookup returns a registered claim snapshot for name.
func (r *Registry) Lookup(name string) (ipc.Claim, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.claims[name]
	if !ok {
		return ipc.Claim{}, false
	}
	c.Targets = append([]route.Target(nil), c.Targets...)
	c.PublicPaths = append([]string(nil), c.PublicPaths...)
	return c, true
}

// Reap removes claims whose owning PID is no longer alive. It returns the
// number of claims dropped. Safe to call concurrently with all other
// Registry methods.
func (r *Registry) Reap() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	dropped := 0
	for name, c := range r.claims {
		if !defaultPIDAlive(c.OwnerPID) {
			delete(r.claims, name)
			dropped++
		}
	}
	return dropped
}

// defaultPIDAlive checks whether pid is alive with a signal-0 probe. EPERM (the
// process exists but we cannot signal it) still counts as alive; for a
// same-user agent it should not happen anyway.
func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

func normalizeClaim(c ipc.Claim) (ipc.Claim, error) {
	if c.Name == "" {
		return ipc.Claim{}, errors.New("name is required")
	}
	if c.OwnerPID == 0 {
		return ipc.Claim{}, errors.New("owner_pid is required")
	}
	targets := c.Targets
	if len(targets) == 0 && c.Port != 0 {
		targets = []route.Target{{Path: "/", Port: c.Port}}
	}
	normalized, err := route.NormalizeTargets(targets)
	if err != nil {
		return ipc.Claim{}, err
	}
	if len(normalized) == 0 {
		return ipc.Claim{}, errors.New("at least one target is required")
	}
	c.Targets = normalized
	c.Port = route.PrimaryPort(normalized)
	c.PublicPaths = nil
	return c, nil
}
