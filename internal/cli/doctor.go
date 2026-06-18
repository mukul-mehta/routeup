package cli

import (
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose routeup setup and connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("doctor: not implemented yet")
			return nil
		},
	}
}
