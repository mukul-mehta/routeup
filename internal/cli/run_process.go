package cli

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

const runnerStartupTimeout = 60 * time.Second

type runnerResult struct {
	code int
	err  error
}

func waitForRunnerTarget(ctx context.Context, port int, results <-chan runnerResult) (runnerResult, bool, error) {
	startupCtx, cancel := context.WithTimeout(ctx, runnerStartupTimeout)
	defer cancel()

	portStr := strconv.Itoa(port)
	// Probe both IPv4 and IPv6 loopback: Node.js 17+ defaults localhost to ::1,
	// so services may bind either address depending on their framework.
	probeAddrs := []string{
		net.JoinHostPort("127.0.0.1", portStr),
		net.JoinHostPort("::1", portStr),
	}
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case result := <-results:
			return result, true, nil
		default:
		}

		if dialAny(startupCtx, &dialer, probeAddrs) {
			select {
			case result := <-results:
				return result, true, nil
			default:
			}
			return runnerResult{}, false, nil
		}

		select {
		case result := <-results:
			return result, true, nil
		case <-startupCtx.Done():
			if ctx.Err() != nil {
				return runnerResult{}, false, ctx.Err()
			}
			return runnerResult{}, false, fmt.Errorf("target did not listen on %s or %s within %s",
				probeAddrs[0], probeAddrs[1], runnerStartupTimeout)
		case <-ticker.C:
		}
	}
}

// dialAny returns true if a TCP connection to any of addrs succeeds.
func dialAny(ctx context.Context, d *net.Dialer, addrs []string) bool {
	for _, addr := range addrs {
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func runnerResultError(result runnerResult, port int, beforeReady bool) error {
	if result.err != nil {
		return fmt.Errorf("run command: %w", result.err)
	}
	if result.code != 0 {
		return &ExitError{Code: result.code}
	}
	if beforeReady {
		return fmt.Errorf("command exited before listening on port %d", port)
	}
	return nil
}
