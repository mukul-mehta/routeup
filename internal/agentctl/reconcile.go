package agentctl

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// reconcileInterval is how often MaintainClaim re-checks that the claim is
// still held by the agent instance it registered with.
const reconcileInterval = 2 * time.Second

// MaintainClaim keeps claim registered with the agent until ctx is cancelled.
//
// The agent holds its registry in memory, so a crash or restart drops every
// route. The serve process is still running and still wants its route, so it
// re-registers when needed instead of registering once and hoping the agent
// remembers.
//
// The loop tracks the agent's BootID, which the agent picks once at startup.
// The same BootID means the same agent that still has our claim; a different
// one (or an unreachable agent) means we lost it and register again.
//
// One tick, every reconcileInterval:
//
//	Status() ─┬─ error ──────────────▶ agent gone: spawn one, then re-register
//	          ├─ ok, BootID changed ──▶ agent restarted: re-register
//	          └─ ok, BootID matches ──▶ nothing to do
//
// After a re-register the loop records the new BootID, so the following ticks
// fall back to the do-nothing case. Messages go to w; the caller unregisters
// when ctx is cancelled.
func (c *Client) MaintainClaim(ctx context.Context, claim ipc.Claim, w io.Writer) {
	bootID := c.currentBootID(ctx)

	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			bootID = c.reconcileTick(ctx, claim, bootID, w)
		}
	}
}

// reconcileTick performs one reconciliation pass and returns the boot id the
// claim is now registered against (unchanged when nothing needed doing).
func (c *Client) reconcileTick(ctx context.Context, claim ipc.Claim, bootID string, w io.Writer) string {
	statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	status, err := c.Status(statusCtx)
	cancel()

	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "routeup: agent unreachable; restarting and re-registering %q\n", claim.Name)
		ensureCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		if _, e := c.EnsureRunning(ensureCtx); e != nil {
			_, _ = fmt.Fprintf(w, "routeup: could not restart agent: %v\n", e)
			return bootID
		}
		return c.restoreClaim(ctx, claim, bootID, w)

	case status.BootID != bootID:
		_, _ = fmt.Fprintf(w, "routeup: agent restarted; re-registering %q\n", claim.Name)
		return c.restoreClaim(ctx, claim, bootID, w)

	default:
		return bootID
	}
}

// restoreClaim re-registers the claim and returns the agent's current boot id
// so the caller can track the (possibly new) instance. On failure it logs and
// keeps the old boot id so the next tick retries.
func (c *Client) restoreClaim(ctx context.Context, claim ipc.Claim, bootID string, w io.Writer) string {
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := c.Register(opCtx, claim); err != nil {
		_, _ = fmt.Fprintf(w, "routeup: re-register failed: %v\n", err)
		return bootID
	}
	return c.currentBootID(opCtx)
}

// currentBootID returns the running agent's boot id, or "" if it can't be read.
// A "" result is safe: the next reconcile tick will see a mismatch and re-sync.
func (c *Client) currentBootID(ctx context.Context) string {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if s, err := c.Status(probeCtx); err == nil {
		return s.BootID
	}
	return ""
}
