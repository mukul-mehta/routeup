package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/state"
)

const (
	servePlanFD  = 3
	serveReadyFD = 4
)

type detachedServeResult struct {
	Ready *routeReadyEvent `json:"ready,omitempty"`
	Error string           `json:"error,omitempty"`
}

func newServeOwnerCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve-owner",
		Short:  "(internal) hold a detached route",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			planFile := os.NewFile(servePlanFD, "routeup-serve-plan")
			readyFile := os.NewFile(serveReadyFD, "routeup-serve-ready")
			if planFile == nil || readyFile == nil {
				return errors.New("detached serve pipes are unavailable")
			}
			defer func() { _ = planFile.Close() }()
			defer func() { _ = readyFile.Close() }()

			var plan servePlan
			if err := json.NewDecoder(planFile).Decode(&plan); err != nil {
				return fmt.Errorf("decode detached serve plan: %w", err)
			}
			encoder := json.NewEncoder(readyFile)
			readySent := false
			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
			defer stop()
			err := runServeOwner(ctx, cmd.Root().Version, plan, false, readyFile, cmd.ErrOrStderr(), func(event routeReadyEvent) error {
				if err := encoder.Encode(detachedServeResult{Ready: &event}); err != nil {
					return fmt.Errorf("send detached serve readiness: %w", err)
				}
				readySent = true
				return readyFile.Close()
			})
			if err != nil && !readySent {
				_ = encoder.Encode(detachedServeResult{Error: err.Error()})
			}
			return err
		},
	}
}

func startDetachedServe(ctx context.Context, _ string, plan servePlan) (routeReadyEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	exe, err := os.Executable()
	if err != nil {
		return routeReadyEvent{}, fmt.Errorf("locate routeup binary: %w", err)
	}
	planRead, planWrite, err := os.Pipe()
	if err != nil {
		return routeReadyEvent{}, fmt.Errorf("create detached serve plan pipe: %w", err)
	}
	defer func() { _ = planRead.Close() }()
	defer func() { _ = planWrite.Close() }()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return routeReadyEvent{}, fmt.Errorf("create detached serve readiness pipe: %w", err)
	}
	defer func() { _ = readyRead.Close() }()
	defer func() { _ = readyWrite.Close() }()

	logPath, err := state.AgentLogPath()
	if err != nil {
		return routeReadyEvent{}, err
	}
	if err := state.EnsureParentDir(logPath); err != nil {
		return routeReadyEvent{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return routeReadyEvent{}, fmt.Errorf("open detached serve log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	child := exec.Command(exe, "serve-owner")
	child.Dir = plan.CWD
	child.Stdin = nil
	child.Stdout = logFile
	child.Stderr = logFile
	child.ExtraFiles = []*os.File{planRead, readyWrite}
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	child.Env = withoutEnv(os.Environ(), "ROUTEUP_TOKEN")
	if err := child.Start(); err != nil {
		return routeReadyEvent{}, fmt.Errorf("start detached route owner: %w", err)
	}
	_ = planRead.Close()
	_ = readyWrite.Close()
	if err := json.NewEncoder(planWrite).Encode(plan); err != nil {
		stopDetachedChild(child)
		return routeReadyEvent{}, fmt.Errorf("send detached serve plan: %w", err)
	}
	_ = planWrite.Close()

	resultCh := make(chan struct {
		result detachedServeResult
		err    error
	}, 1)
	go func() {
		var result detachedServeResult
		err := json.NewDecoder(readyRead).Decode(&result)
		resultCh <- struct {
			result detachedServeResult
			err    error
		}{result: result, err: err}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	select {
	case decoded := <-resultCh:
		if decoded.err != nil {
			stopDetachedChild(child)
			return routeReadyEvent{}, fmt.Errorf("detached route owner exited before readiness: %w", decoded.err)
		}
		if decoded.result.Error != "" {
			stopDetachedChild(child)
			return routeReadyEvent{}, errors.New(decoded.result.Error)
		}
		if decoded.result.Ready == nil {
			stopDetachedChild(child)
			return routeReadyEvent{}, errors.New("detached route owner returned no readiness event")
		}
		_ = child.Process.Release()
		return *decoded.result.Ready, nil
	case <-waitCtx.Done():
		_ = readyRead.Close()
		stopDetachedChild(child)
		return routeReadyEvent{}, fmt.Errorf("wait for detached route owner: %w", waitCtx.Err())
	}
}

func withoutEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if len(entry) >= len(prefix) && entry[:len(prefix)] == prefix {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func stopDetachedChild(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
