// Package process runs child commands for routeup's runner mode.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const defaultKillGrace = 10 * time.Second

// Runner runs one child command. Command uses the system shell; Argv executes
// an explicit argument vector without shell interpolation. Exactly one must be set.
type Runner struct {
	Command string
	Argv    []string
	Dir     string
	Env     []string
	// KillGrace is the delay before SIGKILL. Zero uses ten seconds.
	KillGrace time.Duration
}

// Stdio wires the child's standard streams. A nil field detaches that stream.
type Stdio struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run starts the command and blocks until it exits or ctx is cancelled.
//
// On cancellation it forwards SIGINT or SIGTERM to the process group, waits up
// to KillGrace, then sends SIGKILL. Programmatic cancellation uses SIGTERM.
// Non-zero child exits are returned as codes, not errors.
func (r Runner) Run(ctx context.Context, stdio Stdio) (code int, runErr error) {
	if err := ctx.Err(); err != nil {
		return 1, err
	}
	grace := r.KillGrace
	if grace <= 0 {
		grace = defaultKillGrace
	}

	cmd, err := r.execCommand()
	if err != nil {
		return 1, err
	}
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	cmd.Stdin = stdio.In
	cmd.Stdout = stdio.Out
	cmd.Stderr = stdio.Err
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	ttyFD := -1
	if stdin, ok := stdio.In.(*os.File); ok && term.IsTerminal(int(stdin.Fd())) {
		ttyFD = int(stdin.Fd())
		cmd.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Ctty: ttyFD}
	}

	if err := cmd.Start(); err != nil {
		if ttyFD >= 0 {
			if restoreErr := restoreForeground(ttyFD); restoreErr != nil {
				return 1, errors.Join(fmt.Errorf("start command: %w", err), restoreErr)
			}
		}
		return 1, fmt.Errorf("start command: %w", err)
	}
	if ttyFD >= 0 {
		defer func() {
			if err := restoreForeground(ttyFD); err != nil && runErr == nil {
				code = 1
				runErr = err
			}
		}()
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case err := <-waitCh:
		if stopErr := terminateRemainingProcessGroup(cmd.Process.Pid, grace); stopErr != nil {
			return 1, stopErr
		}
		return exitCode(err), nil
	case <-ctx.Done():
	}

	if err := signalProcessGroup(cmd.Process.Pid, cancellationSignal(ctx)); err != nil {
		_ = cmd.Process.Kill() // Best effort after the group signal failed.
		<-waitCh
		return 1, err
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	var waitErr error
	leaderExited := false
	for {
		select {
		case waitErr = <-waitCh:
			leaderExited = true
			waitCh = nil
		case <-ticker.C:
			if leaderExited && !processGroupAlive(cmd.Process.Pid) {
				return exitCode(waitErr), nil
			}
		case <-timer.C:
			if err := signalProcessGroup(cmd.Process.Pid, syscall.SIGKILL); err != nil {
				if !leaderExited {
					_ = cmd.Process.Kill() // Best effort after the group signal failed.
					<-waitCh
				}
				return 1, err
			}
			if !leaderExited {
				waitErr = <-waitCh
			}
			return exitCode(waitErr), nil
		}
	}
}

func (r Runner) execCommand() (*exec.Cmd, error) {
	command := strings.TrimSpace(r.Command)
	if command != "" && len(r.Argv) > 0 {
		return nil, errors.New("set command or argv, not both")
	}
	if len(r.Argv) > 0 {
		if strings.TrimSpace(r.Argv[0]) == "" {
			return nil, errors.New("executable is required")
		}
		// The shell performs PATH lookup using the child's injected environment;
		// "$@" preserves the explicit argument boundaries without interpolation.
		args := []string{"-c", `exec "$@"`, "routeup-exec"}
		args = append(args, r.Argv...)
		return exec.Command("sh", args...), nil
	}
	if command == "" {
		return nil, errors.New("no command to run")
	}
	return exec.Command("sh", "-c", command), nil
}

func signalProcessGroup(pid int, signal syscall.Signal) error {
	err := syscall.Kill(-pid, signal)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("signal process group %d: %w", pid, err)
}

func processGroupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminateRemainingProcessGroup(pid int, grace time.Duration) error {
	if !processGroupAlive(pid) {
		return nil
	}
	if err := signalProcessGroup(pid, syscall.SIGTERM); err != nil {
		return err
	}

	deadline := time.Now().Add(grace)
	for processGroupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if !processGroupAlive(pid) {
		return nil
	}
	return signalProcessGroup(pid, syscall.SIGKILL)
}

func restoreForeground(fd int) error {
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, syscall.Getpgrp()); err != nil {
		return fmt.Errorf("restore terminal foreground process group: %w", err)
	}
	return nil
}

// exitCode extracts a process exit code from the error returned by cmd.Wait.
// A signal-terminated child reports 128+signal, matching shell convention.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return 1
}
