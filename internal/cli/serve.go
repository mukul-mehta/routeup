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
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

// newServeCmd builds `routeup serve`.
func newServeCmd() *cobra.Command {
	var (
		portFlag int
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "serve [name]",
		Short: "Serve a local app on a stable HTTPS route",
		Long: "Serve a local app on https://<name>.localhost.\n\n" +
			"The route name comes from the argument, or from routeup.json or the\n" +
			"package.json \"routeup\" block when omitted. A bare name is prefixed\n" +
			"with the project name; a dotted name is taken literally:\n\n" +
			"  serve myapp      ->  https://myapp.localhost\n" +
			"  serve api        ->  https://api.<project>.localhost\n" +
			"  serve api.myapp  ->  https://api.myapp.localhost",
		Example: "  routeup serve myapp --port 3000\n" +
			"  routeup serve api.myapp --port 8080\n" +
			"  routeup serve myapp --port 3000 --dry-run",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runServe(cmd, args, cwd, os.Getenv, portFlag, dryRun)
		},
	}

	cmd.Flags().IntVar(&portFlag, "port", 0, "port your local app listens on")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved route and exit")

	return cmd
}

// runServe is the body of `routeup serve`. cwd and getenv are parameters
// so route resolution stays deterministic.
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

	tlsPort := state.TLSPortOrDefault()

	out := cmd.OutOrStdout()
	if dryRun {
		_, _ = fmt.Fprintf(out, "route: %s\n", resolved.Route)
		_, _ = fmt.Fprintf(out, "local: %s\n", localURL(resolved.Route.LocalHost(), tlsPort))
		_, _ = fmt.Fprintf(out, "public: https://%s\n", resolved.Route.PublicHost())
		_, _ = fmt.Fprintf(out, "target: http://localhost:%d\n", resolved.Port)
		return nil
	}

	// Preflight so we don't wait for an agent-spawn timeout on a missing CA.
	if err := preflightCA(); err != nil {
		return err
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
	_, _ = fmt.Fprintf(out, "local: %s\n", localURL(resolved.Route.LocalHost(), tlsPort))
	_, _ = fmt.Fprintf(out, "target: http://localhost:%d\n", resolved.Port)
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "press Ctrl-C to stop")

	// Re-register if the agent restarts. Blocks until ctx cancels.
	client.MaintainClaim(ctx, claim, cmd.ErrOrStderr())
	return nil
}

// localURL omits :443.
func localURL(host string, port int) string {
	if port == 443 {
		return fmt.Sprintf("https://%s", host)
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}

// preflightCA wraps certs.EnsureCA with a "setup incomplete:" prefix.
func preflightCA() error {
	certPath, err := state.CACertPath()
	if err != nil {
		return err
	}
	keyPath, err := state.CAKeyPath()
	if err != nil {
		return err
	}
	if _, err := certs.EnsureCA(certPath, keyPath); err != nil {
		return fmt.Errorf("setup incomplete: %w", err)
	}
	return nil
}
