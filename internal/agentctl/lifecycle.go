package agentctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

// EnsureResult reports what EnsureRunning did to satisfy the request.
type EnsureResult int

const (
	// EnsureAlreadyRunning means a healthy, current agent was already up.
	EnsureAlreadyRunning EnsureResult = iota
	// EnsureStarted means no agent was running and one was spawned.
	EnsureStarted
	// EnsureRestarted means a stale agent was stopped and replaced.
	EnsureRestarted
)

func (r EnsureResult) String() string {
	switch r {
	case EnsureAlreadyRunning:
		return "already running"
	case EnsureStarted:
		return "started"
	case EnsureRestarted:
		return "restarted"
	default:
		return "unknown"
	}
}

// EnsureRunning guarantees a current agent is reachable, spawning or
// restarting one as needed.
//
//   - If a healthy agent matching this build is up, returns EnsureAlreadyRunning.
//   - If no agent is up, spawns one and returns EnsureStarted.
//   - If a stale agent is up (old version or rebuilt binary), stops it, spawns
//     a fresh one, and returns EnsureRestarted.
//
// The spawn path re-execs the routeup binary with the hidden "agent run"
// subcommand, detaches it via setsid, redirects stdio to the agent log file,
// and polls /v1/status until it responds (default 5 s budget).
func (c *Client) EnsureRunning(ctx context.Context) (EnsureResult, error) {
	if status, err := c.Status(ctx); err == nil {
		stale, _ := c.IsStale(status)
		if !stale {
			return EnsureAlreadyRunning, nil
		}
		if _, err := c.Stop(ctx); err != nil {
			return 0, fmt.Errorf("stop stale agent: %w", err)
		}
		if err := c.spawnAndWait(ctx); err != nil {
			return 0, err
		}
		return EnsureRestarted, nil
	}

	// No reachable agent. Spawn one. If the spawn fails because a stray agent
	// is still holding the proxy port (its socket gone, so Status couldn't see
	// it), stop that stray via its PID file and try once more.
	if err := c.spawnAndWait(ctx); err != nil {
		stopped, _ := c.Stop(ctx)
		if !stopped {
			return 0, err
		}
		if err := c.spawnAndWait(ctx); err != nil {
			return 0, err
		}
	}
	return EnsureStarted, nil
}

// Stop stops the running agent and reports whether one was actually running.
//
// It tries a graceful shutdown over the socket first. If the socket is gone
// (for example it was deleted while the agent kept running and held the proxy
// port), it falls back to the PID file and signals the process directly.
func (c *Client) Stop(ctx context.Context) (bool, error) {
	if _, err := c.Status(ctx); err != nil {
		return c.stopByPIDFile(ctx)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+ipc.PathShutdown, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	if resp, derr := c.httpClient.Do(req); derr == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if err := c.waitDown(ctx, 5*time.Second); err != nil {
		return false, err
	}
	return true, nil
}

// stopByPIDFile signals the agent named in the PID file and waits for it to
// exit (releasing the proxy port). It reports whether a live agent was found.
func (c *Client) stopByPIDFile(ctx context.Context) (bool, error) {
	pid, ok := readAgentPID()
	if !ok || !processAlive(pid) {
		removeAgentPIDFile()
		return false, nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("find agent process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return false, fmt.Errorf("signal agent process %d: %w", pid, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			return false, fmt.Errorf("agent process %d did not exit after SIGTERM", pid)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	removeAgentPIDFile()
	return true, nil
}

// Restart stops any running agent and starts a fresh one.
func (c *Client) Restart(ctx context.Context) error {
	if _, err := c.Stop(ctx); err != nil {
		return err
	}
	return c.spawnAndWait(ctx)
}

// IsStale reports whether the running agent described by s differs from this
// CLI's build: a different version string, or a binary rebuilt since the agent
// started. The second return value is a short reason, empty when not stale.
//
// It is conservative: when either side lacks a signal (empty version or zero
// mod time) that signal is skipped rather than treated as a mismatch.
func (c *Client) IsStale(s ipc.Status) (bool, string) {
	if c.version != "" && s.Version != "" && s.Version != c.version {
		return true, fmt.Sprintf("agent version %s, CLI version %s", s.Version, c.version)
	}
	localMod := execModTime()
	if !localMod.IsZero() && !s.ExecModTime.IsZero() && !localMod.Equal(s.ExecModTime) {
		return true, "binary was rebuilt since the agent started"
	}
	return false, ""
}

func (c *Client) spawnAndWait(ctx context.Context) error {
	if err := c.spawn(); err != nil {
		return fmt.Errorf("spawn agent: %w", err)
	}
	if err := c.waitReady(ctx, 5*time.Second); err != nil {
		// The agent runs detached and logs its own startup failure, so a port
		// clash or similar would otherwise be invisible behind the timeout.
		if tail := agentLogTail(); tail != "" {
			return fmt.Errorf("%w\nagent log:\n%s", err, tail)
		}
		return err
	}
	return nil
}

func (c *Client) spawn() error {
	exe := c.execPath
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("locate binary: %w", err)
		}
	}

	logPath, err := state.AgentLogPath()
	if err != nil {
		return err
	}
	if err := state.EnsureParentDir(logPath); err != nil {
		return err
	}

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open agent log %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, "agent", "run")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Inherit environment so ROUTEUP_AGENT_SOCKET (and friends) propagate.
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	// Don't wait; we want it detached.
	_ = cmd.Process.Release()
	return nil
}

func (c *Client) waitReady(ctx context.Context, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		_, err := c.Status(probeCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent did not become ready within %s (last error: %w)", budget, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *Client) waitDown(ctx context.Context, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		_, err := c.Status(probeCtx)
		cancel()
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent still reachable after %s", budget)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// execModTime returns the modification time of this CLI's own binary, or the
// zero time if it can't be determined. A mismatch against the agent's
// startup-captured value means the binary was rebuilt.
func execModTime() time.Time {
	exe, err := os.Executable()
	if err != nil {
		return time.Time{}
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// readAgentPID reads the PID recorded by a running agent.
func readAgentPID() (int, bool) {
	path, err := state.AgentPIDPath()
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func removeAgentPIDFile() {
	if path, err := state.AgentPIDPath(); err == nil {
		_ = os.Remove(path)
	}
}

// processAlive reports whether pid names a running process. A signal-0 probe
// returns EPERM for a process owned by another user, which still counts as
// alive.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// agentLogTail returns the last few lines of the agent log so a detached
// agent's startup failure surfaces in the CLI.
func agentLogTail() string {
	path, err := state.AgentLogPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	const keep = 6
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	return strings.Join(lines, "\n")
}
