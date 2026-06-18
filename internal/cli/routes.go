package cli

import (
	"github.com/spf13/cobra"
)

func newRoutesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "routes",
		Short: "List all routes currently active",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("routes: not implemented yet")
			return nil
		},
	}
}
