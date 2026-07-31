package cli

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

const runnerStartupTimeout = 15 * time.Second

type runnerResult struct {
	code int
	err  error
}

func waitForRunnerTarget(ctx context.Context, port int, results <-chan runnerResult) (runnerResult, bool, error) {
	startupCtx, cancel := context.WithTimeout(ctx, runnerStartupTimeout)
	defer cancel()

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case result := <-results:
			return result, true, nil
		default:
		}

		conn, err := dialer.DialContext(startupCtx, "tcp", address)
		if err == nil {
			_ = conn.Close() // The readiness probe carries no data.
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
			return runnerResult{}, false, fmt.Errorf("target did not listen on %s within %s", address, runnerStartupTimeout)
		case <-ticker.C:
		}
	}
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
