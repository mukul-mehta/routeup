package cli

import (
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Stream local and public request logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("logs: not implemented yet")
			return nil
		},
	}
}
