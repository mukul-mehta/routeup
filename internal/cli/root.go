// Package cli wires the routeup command tree.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Execute runs the routeup root command and returns any error from the command tree.
func Execute() error {
	return newRootCmd().Execute()
}

// version is the routeup build version. Overridden at release time via
// -ldflags -X (must be a var, not a const). The agent reports this string,
// and the CLI compares it against a running agent to decide whether to
// restart a stale build.
var version = "0.0.0-dev"

func newRootCmd() *cobra.Command {
	const (
		groupStart   = "start"
		groupObserve = "observe"
		groupManage  = "manage"
	)
	root := &cobra.Command{
		Use:   "routeup",
		Short: "Stable HTTPS routes for local services",
		Long: "routeup gives local services stable HTTPS names like\n" +
			"https://example-app.localhost, and can expose those same routes publicly\n" +
			"when you need to.\n\n" +
			"Run `routeup setup` once to create and trust a local CA and bind\n" +
			"port 443, then `routeup serve <name> --port <p>` to put a local app\n" +
			"on a trusted HTTPS route.",
		Example: "  # one-time machine setup: local CA, OS trust, port 443\n" +
			"  routeup setup\n\n" +
			"  # serve a local app on https://example-app.localhost\n" +
			"  routeup serve example-app --port 3000\n\n" +
			"  # run your dev server on a stable route (Portless mode)\n" +
			"  routeup\n\n" +
			"  # list what's currently served\n" +
			"  routeup routes",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		// Bare `routeup` is the script runner: it wraps the configured dev
		// command. Any positional means a mistyped subcommand, so reject it.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runRun(cmd, cwd)
		},
	}
	root.AddGroup(
		&cobra.Group{ID: groupStart, Title: "Start:"},
		&cobra.Group{ID: groupObserve, Title: "Observe:"},
		&cobra.Group{ID: groupManage, Title: "Manage:"},
	)
	root.SetHelpCommandGroupID(groupManage)
	root.SetCompletionCommandGroupID(groupManage)
	root.AddCommand(
		commandGroup(groupStart, newServeCmd()),
		commandGroup(groupStart, newExposeCmd()),
		commandGroup(groupObserve, newDashboardCmd()),
		commandGroup(groupObserve, newRoutesCmd()),
		commandGroup(groupObserve, newLogsCmd()),
		commandGroup(groupObserve, newInspectCmd()),
		commandGroup(groupObserve, newDoctorCmd()),
		commandGroup(groupObserve, newConfigCmd()),
		commandGroup(groupManage, newAgentCmd()),
		commandGroup(groupManage, newSetupCmd()),
		commandGroup(groupManage, newUninstallCmd()),
		commandGroup(groupManage, newUpdateCmd()),
		commandGroup(groupManage, newVersionCmd()),
		commandGroup(groupManage, newForwardCmd()),
		commandGroup(groupManage, newTokenCmd()),
		commandGroup(groupManage, newServerCmd()),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the routeup version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			styles := newTerminalStyles(out)
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("routeup"), styles.accent(cmd.Root().Version))
		},
	}
}

func commandGroup(group string, cmd *cobra.Command) *cobra.Command {
	cmd.GroupID = group
	return cmd
}
