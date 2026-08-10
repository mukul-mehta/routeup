package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/state"
)

// newRoutesCmd lists active routes by querying the local agent. If the agent
// is not running, nothing is active by definition, and the command says so
// without spawning one (queries shouldn't have side effects).
func newRoutesCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
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

			claims, err := client.List(ctx)
			if agentctl.IsUnavailable(err) {
				if jsonOutput {
					return json.NewEncoder(out).Encode([]ipc.Claim{})
				}
				_, _ = fmt.Fprintln(out, "no active routes (agent not running)")
				return nil
			}
			if err != nil {
				return fmt.Errorf("list active routes: %w", err)
			}
			if len(claims) == 0 {
				if jsonOutput {
					return json.NewEncoder(out).Encode([]ipc.Claim{})
				}
				_, _ = fmt.Fprintln(out, "no active routes")
				return nil
			}
			if jsonOutput {
				if err := json.NewEncoder(out).Encode(claims); err != nil {
					return fmt.Errorf("write routes json: %w", err)
				}
				return nil
			}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "NAME\tTARGETS\tPUBLIC\tPATHS\tPID\tAGE\tCWD")
			now := time.Now()
			for _, c := range claims {
				public, paths := "-", "-"
				if c.PublicHost != "" {
					public, paths = "https://"+c.PublicHost, formatExposePaths(c.PublicPaths)
					if c.PublicState == ipc.ExposureReconnecting {
						public += " (reconnecting)"
					}
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
					terminalEscapeString(c.Name), terminalEscapeString(formatTargets(c.Targets)),
					terminalEscapeString(public), terminalEscapeString(paths), c.OwnerPID,
					humanDuration(now.Sub(c.RegisteredAt)), terminalEscapeString(c.OwnerCWD))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write active routes as JSON")
	return cmd
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
