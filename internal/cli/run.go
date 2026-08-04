package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/process"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/state"
)

// ExitError carries a child exit status to the CLI entry point.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

func runRun(cmd *cobra.Command, cwd string) error {
	discovered, err := config.Discover(cwd)
	if err != nil {
		return fmt.Errorf("discover config: %w", err)
	}
	file := discovered.Config

	command := strings.TrimSpace(file.Command)
	if command == "" {
		return errors.New("nothing to run: set \"script\" in your package.json routeup block or \"command\" in routeup.json (or use `routeup serve`)")
	}

	routeName, err := runnerRoute(file)
	if err != nil {
		return err
	}

	targets, appPort, err := runnerTargets(file)
	if err != nil {
		return fmt.Errorf("resolve targets: %w", err)
	}

	if err := certs.EnsureLocalCA(); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	tlsPort := state.TLSPortOrDefault()

	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := process.NotifyContext(parent)
	defer stop()

	startCtx, cancelStart := context.WithTimeout(ctx, 12*time.Second)
	defer cancelStart()
	if _, err := client.EnsureRunning(startCtx); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	if err := process.EnsurePortAvailable(appPort); err != nil {
		return err
	}

	claim := ipc.Claim{
		Name:     routeName.String(),
		Port:     appPort,
		Targets:  targets,
		Capture:  file.Capture,
		OwnerPID: os.Getpid(),
		OwnerCWD: cwd,
	}
	if _, err := client.Register(startCtx, claim); err != nil {
		if _, ok := errors.AsType[*ipc.ConflictError](err); ok {
			return fmt.Errorf("%w\n  hint: stop the holding process or pick a different route name", err)
		}
		return fmt.Errorf("register route: %w", err)
	}

	maintainCtx, cancelMaintain := context.WithCancel(ctx)
	maintainDone := make(chan struct{})
	go func() {
		defer close(maintainDone)
		client.MaintainClaim(maintainCtx, claim, errOut)
	}()

	defer func() {
		cancelMaintain()
		<-maintainDone
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Unregister(shutdownCtx, claim.Name); err != nil {
			_, _ = fmt.Fprintf(errOut, "routeup: unregister %q: %v\n", claim.Name, err)
		}
	}()

	local := localURL(routeName.LocalHost(), tlsPort)
	childEnv := process.InjectEnv(os.Environ(), process.EnvInputs{
		Port:     appPort,
		Host:     "127.0.0.1",
		LocalURL: local,
		WorkDir:  cwd,
	})

	_, _ = fmt.Fprintf(out, "running: %s\n", command)
	_, _ = fmt.Fprintln(out, "")

	runner := process.Runner{Command: command, Dir: cwd, Env: childEnv}
	childCtx, cancelChild := context.WithCancel(ctx)
	resultCh := make(chan runnerResult, 1)
	go func() {
		code, runErr := runner.Run(childCtx, process.Stdio{
			In:  cmd.InOrStdin(),
			Out: out,
			Err: errOut,
		})
		resultCh <- runnerResult{code: code, err: runErr}
	}()

	result, exited, readyErr := waitForRunnerTarget(ctx, appPort, resultCh)
	if readyErr != nil {
		cancelChild()
		result = <-resultCh
		if ctx.Err() != nil {
			return runnerResultError(result, appPort, false)
		}
		if result.err != nil {
			return runnerResultError(result, appPort, false)
		}
		return readyErr
	}
	if exited {
		cancelChild()
		return runnerResultError(result, appPort, true)
	}

	_, _ = fmt.Fprintf(out, "route: %s\n", routeName)
	_, _ = fmt.Fprintf(out, "local: %s\n", local)
	printTargets(out, targets)
	_, _ = fmt.Fprintln(out, "")

	result = <-resultCh
	cancelChild()
	return runnerResultError(result, appPort, false)
}

func runnerRoute(file config.Config) (route.Name, error) {
	name := strings.TrimSpace(os.Getenv("ROUTEUP_NAME"))
	if name == "" {
		name = file.Name
	}
	if name == "" {
		return route.Name{}, errors.New("no route name: set \"name\" in your routeup config or ROUTEUP_NAME")
	}
	parsed, err := route.Parse(name)
	if err != nil {
		return route.Name{}, fmt.Errorf("invalid route name: %w", err)
	}
	return parsed, nil
}

func runnerTargets(file config.Config) ([]route.Target, int, error) {
	var base []route.Target
	hasConfiguredTargets := file.Port != 0 || len(file.Targets) > 0 || strings.TrimSpace(os.Getenv("ROUTEUP_PORT")) != ""
	if hasConfiguredTargets {
		var err error
		base, _, err = config.ResolveTargets(config.Inputs{Env: os.Getenv, File: file})
		if err != nil {
			return nil, 0, err
		}
	}

	rootPort := 0
	for _, target := range base {
		if target.Path == "/" {
			rootPort = target.Port
		}
	}
	if rootPort == 0 {
		port, err := process.FreePort()
		if err != nil {
			return nil, 0, err
		}
		rootPort = port
		base = append(base, route.Target{Path: "/", Port: rootPort})
	}

	targets, err := route.NormalizeTargets(base)
	if err != nil {
		return nil, 0, err
	}
	return targets, rootPort, nil
}
