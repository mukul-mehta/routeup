package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

// newServeCmd builds the `routeup serve` command.
//
// Flags:
//
//	--port    target port for the local service
//	--dry-run print the resolved route and exit without registering
//
// Args:
//
//	[name]    optional route name; bare names are prefixed with the project
//	          name from the closest config (see config.Resolve).
func newServeCmd() *cobra.Command {
	var (
		portFlag int
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "serve [name]",
		Short: "Serve a route locally via the routeup agent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runServe(cmd, args, cwd, os.Getenv, portFlag, dryRun)
		},
	}

	cmd.Flags().IntVar(&portFlag, "port", 0, "target port for the local service")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved route and exit without registering")

	return cmd
}

// runServe is the body of `routeup serve`. cwd and getenv are parameters
// rather than globals so route resolution stays deterministic.
//
// Two paths:
//
//   - --dry-run: resolve the route + target, print local/public URLs, exit.
//   - default:   ensure the agent is running, register the route, print
//     status, block on SIGINT/SIGTERM, then unregister on the way out.
func runServe(cmd *cobra.Command, args []string, cwd string, getenv func(string) string, port int, dryRun bool) error {
	discovered, err := config.Discover(cwd)
	if err != nil {
		return err
	}

	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}

	resolved, err := config.Resolve(config.Inputs{
		PositionalName: positional,
		PortFlag:       port,
		Env:            getenv,
		File:           discovered.Config,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if dryRun {
		_, _ = fmt.Fprintf(out, "route: %s\n", resolved.Route)
		_, _ = fmt.Fprintf(out, "local: https://%s\n", resolved.Route.LocalHost())
		_, _ = fmt.Fprintf(out, "public: https://%s\n", resolved.Route.PublicHost())
		_, _ = fmt.Fprintf(out, "target: http://localhost:%d\n", resolved.Port)
		return nil
	}

	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	startCtx, cancelStart := context.WithTimeout(ctx, 12*time.Second)
	defer cancelStart()
	ensured, err := client.EnsureRunning(startCtx)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	if ensured == agentctl.EnsureRestarted {
		_, _ = fmt.Fprintln(out, "note: restarted the local agent to pick up a new build")
	}

	claim := ipc.Claim{
		Name:     resolved.Route.String(),
		Port:     resolved.Port,
		OwnerPID: os.Getpid(),
		OwnerCWD: cwd,
	}

	if _, err := client.Register(startCtx, claim); err != nil {
		if _, ok := errors.AsType[*ipc.ConflictError](err); ok {
			return fmt.Errorf("%w\n  hint: stop the holding process or pick a different route name", err)
		}
		return err
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Unregister(shutdownCtx, claim.Name)
	}()

	_, _ = fmt.Fprintf(out, "route: %s\n", resolved.Route)
	_, _ = fmt.Fprintf(out, "local: http://%s:%d\n", resolved.Route.LocalHost(), ipc.DefaultProxyPort)
	_, _ = fmt.Fprintf(out, "target: http://localhost:%d\n", resolved.Port)
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "press Ctrl-C to stop")

	// Keep the claim alive until Ctrl-C: re-register if the agent restarts or
	// disappears while we run. Blocks until ctx is cancelled.
	client.MaintainClaim(ctx, claim, cmd.ErrOrStderr())
	return nil
}
