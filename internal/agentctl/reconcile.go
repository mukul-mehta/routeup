package agentctl

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

const (
	reconcileInterval  = 2 * time.Second
	maxExposureBackoff = 30 * time.Second
)

// DesiredState is the local claim and public exposure a foreground command owns.
// The exact exposure request remains in the CLI process so tokens are never
// persisted by the agent.
type DesiredState struct {
	Claim      *ipc.Claim
	Exposure   *ipc.ExposeRequest
	PublicHost string
}

// Maintain restores desired state after an agent restart or terminal tunnel
// failure. It blocks until ctx is cancelled.
func (c *Client) Maintain(ctx context.Context, desired DesiredState, w io.Writer) {
	bootID, exposureFailed := c.reconcileDesired(ctx, desired, "", w)
	exposureBackoff := reconcileInterval
	nextExposureAttempt := time.Time{}
	if exposureFailed {
		nextExposureAttempt = time.Now().Add(exposureBackoff)
	}
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attemptExposure := nextExposureAttempt.IsZero() || !time.Now().Before(nextExposureAttempt)
			tickDesired := desired
			if !attemptExposure {
				tickDesired.Exposure = nil
			}
			bootID, exposureFailed = c.reconcileDesired(ctx, tickDesired, bootID, w)
			if !attemptExposure {
				continue
			}
			if exposureFailed {
				nextExposureAttempt = time.Now().Add(exposureBackoff)
				exposureBackoff *= 2
				if exposureBackoff > maxExposureBackoff {
					exposureBackoff = maxExposureBackoff
				}
				continue
			}
			nextExposureAttempt = time.Time{}
			exposureBackoff = reconcileInterval
		}
	}
}

// MaintainClaim keeps the existing claim-only API for local-only callers.
func (c *Client) MaintainClaim(ctx context.Context, claim ipc.Claim, w io.Writer) {
	c.Maintain(ctx, DesiredState{Claim: &claim}, w)
}

func (c *Client) reconcileDesired(ctx context.Context, desired DesiredState, bootID string, w io.Writer) (string, bool) {
	if ctx.Err() != nil {
		return bootID, false
	}
	statusCtx, cancelStatus := context.WithTimeout(ctx, 3*time.Second)
	status, err := c.Status(statusCtx)
	cancelStatus()
	if err != nil {
		if ctx.Err() != nil {
			return bootID, false
		}
		if !IsUnavailable(err) {
			_, _ = fmt.Fprintf(w, "routeup: agent status check failed: %v\n", err)
			return bootID, false
		}
		_, _ = fmt.Fprintln(w, "routeup: agent stopped; restarting")
		ensureCtx, cancelEnsure := context.WithTimeout(ctx, 12*time.Second)
		_, ensureErr := c.EnsureRunning(ensureCtx)
		cancelEnsure()
		if ensureErr != nil {
			_, _ = fmt.Fprintf(w, "routeup: could not restart agent: %v\n", ensureErr)
			return bootID, false
		}
		statusCtx, cancelStatus = context.WithTimeout(ctx, 3*time.Second)
		status, err = c.Status(statusCtx)
		cancelStatus()
		if err != nil {
			_, _ = fmt.Fprintf(w, "routeup: could not read restarted agent status: %v\n", err)
			return bootID, false
		}
	}

	restarted := bootID == "" || status.BootID != bootID
	if restarted && desired.Claim != nil {
		opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := c.Register(opCtx, *desired.Claim)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(w, "routeup: re-register %q failed: %v\n", desired.Claim.Name, err)
			return bootID, false
		}
		if bootID != "" {
			_, _ = fmt.Fprintf(w, "routeup: agent restarted; restored route %q\n", desired.Claim.Name)
		}
	}

	if desired.Exposure != nil && !hasExposure(status, *desired.Exposure, desired.PublicHost) {
		opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		response, exposeErr := c.Expose(opCtx, *desired.Exposure)
		cancel()
		if exposeErr != nil {
			_, _ = fmt.Fprintf(w, "routeup: restore public exposure failed: %v\n", exposeErr)
			return status.BootID, true
		}
		if desired.PublicHost != "" && response.Host != desired.PublicHost {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = c.Unexpose(cleanupCtx, ipc.UnexposeRequest{
				Host: response.Host, Route: desired.Exposure.Route, OwnerPID: desired.Exposure.OwnerPID,
			})
			cleanupCancel()
			_, _ = fmt.Fprintf(w, "routeup: restored exposure changed host from %s to %s; released replacement\n", desired.PublicHost, response.Host)
			return status.BootID, true
		}
		_, _ = fmt.Fprintf(w, "routeup: restored public exposure at https://%s\n", response.Host)
	}

	return status.BootID, false
}

func hasExposure(status ipc.Status, req ipc.ExposeRequest, host string) bool {
	for _, exposure := range status.Exposures {
		if exposure.OwnerPID == req.OwnerPID && exposure.Route == req.Route && (host == "" || exposure.Host == host) {
			return true
		}
	}
	return false
}
