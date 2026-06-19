package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/state"
)

// newRoutesCmd lists active routes by querying the local agent. If the agent
// is not running, nothing is active by definition, and the command says so
// without spawning one (queries shouldn't have side effects).
func newRoutesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "routes",
		Short: "List active routes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sockPath, err := state.AgentSocketPath()
			if err != nil {
				return err
			}
			client := agentctl.NewClient(sockPath, "", cmd.Root().Version)

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()

			out := cmd.OutOrStdout()

			if _, err := client.Status(ctx); err != nil {
				_, _ = fmt.Fprintln(out, "no active routes (agent not running)")
				return nil
			}

			claims, err := client.List(ctx)
			if err != nil {
				return err
			}
			if len(claims) == 0 {
				_, _ = fmt.Fprintln(out, "no active routes")
				return nil
			}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "NAME\tPORT\tPID\tAGE\tCWD")
			now := time.Now()
			for _, c := range claims {
				_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n",
					c.Name, c.Port, c.OwnerPID, humanDuration(now.Sub(c.RegisteredAt)), c.OwnerCWD)
			}
			return tw.Flush()
		},
	}
}

func humanDuration(d time.Duration) string {
	if d < time.Second {
		return "1s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
