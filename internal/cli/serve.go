package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/config"
)

// newServeCmd builds the `routeup serve` command.
//
// Flags:
//
//	--port    target port for the local service
//	--dry-run print the resolved route and exit without exposing
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
		Short: "Serve a route (local; --expose in Phase 7 adds public)",
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved route and exit without exposing")

	return cmd
}

// runServe is the testable body of `routeup serve`. cwd and getenv are
// injected so tests can run against a temp directory and a fake env.
//
// On --dry-run, it prints the resolved route, local URL, public URL, and target
// to cmd.OutOrStdout(). Without --dry-run, it prints a "not implemented yet"
// message (the real exposure path is Phase 3+).
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
	if !dryRun {
		_, _ = fmt.Fprintln(out, "serve: not implemented yet (try --dry-run)")
		return nil
	}

	_, _ = fmt.Fprintf(out, "route: %s\n", resolved.Route)
	_, _ = fmt.Fprintf(out, "local: https://%s\n", resolved.Route.LocalHost())
	_, _ = fmt.Fprintf(out, "public: https://%s\n", resolved.Route.PublicHost())
	_, _ = fmt.Fprintf(out, "target: http://127.0.0.1:%d\n", resolved.Port)
	return nil
}
