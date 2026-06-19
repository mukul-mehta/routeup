// Package cli wires the routeup command tree.
package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the routeup root command and returns any error from the command tree.
func Execute() error {
	return newRootCmd().Execute()
}

// version is the routeup build version. Overridden at release time via
// -ldflags. The agent reports this string, and the CLI compares it against a
// running agent to decide whether to restart a stale build.
const version = "0.0.0-dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "routeup",
		Short:         "Stable HTTPS route names for local services",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(
		newDoctorCmd(),
		newRoutesCmd(),
		newLogsCmd(),
		newServeCmd(),
		newAgentCmd(),
	)
	return root
}
