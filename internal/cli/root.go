// Package cli wires the routeup command tree.
package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the routeup root command and returns any error from the command tree.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "routeup",
		Short:         "Stable HTTPS route names for local services",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       "0.0.0-dev",
	}
	root.AddCommand(newDoctorCmd(), newRoutesCmd(), newLogsCmd())
	return root
}
