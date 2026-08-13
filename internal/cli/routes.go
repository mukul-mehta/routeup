package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
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
				_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("no active routes (agent not running)"))
				return nil
			}
			if err != nil {
				return fmt.Errorf("list active routes: %w", err)
			}
			if len(claims) == 0 {
				if jsonOutput {
					return json.NewEncoder(out).Encode([]ipc.Claim{})
				}
				_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("no active routes"))
				return nil
			}
			if jsonOutput {
				if err := json.NewEncoder(out).Encode(claims); err != nil {
					return fmt.Errorf("write routes json: %w", err)
				}
				return nil
			}

			return writeRouteBlocks(out, claims, state.TLSPortOrDefault(), time.Now())
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write active routes as JSON")
	return cmd
}

func writeRouteBlocks(out io.Writer, claims []ipc.Claim, tlsPort int, now time.Time) error {
	styles := newTerminalStyles(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", styles.accent("routes"), styles.muted(fmt.Sprintf("%d active", len(claims))))
	for _, claim := range claims {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, styles.accent(terminalEscapeString(claim.Name)))

		local := "-"
		if name, err := route.Parse(claim.Name); err == nil {
			local = localURL(name.LocalHost(), tlsPort)
		}
		_, _ = fmt.Fprintf(out, "  %s %s\n", styles.label("local   "), styles.url(local))

		for index, target := range claim.Targets {
			label := "        "
			if index == 0 {
				label = styles.label("targets ")
			}
			_, _ = fmt.Fprintf(out, "  %s %s -> %s\n", label, styles.accent(terminalEscapeString(target.Path)), styles.muted(fmt.Sprintf("localhost:%d", target.Port)))
		}

		public := styles.muted("-")
		if claim.PublicHost != "" {
			public = "https://" + terminalEscapeString(claim.PublicHost)
			if claim.PublicState == ipc.ExposureReconnecting {
				public = styles.warning(public + " (reconnecting)")
			} else {
				public = styles.url(public)
			}
		}
		_, _ = fmt.Fprintf(out, "  %s %s\n", styles.label("public  "), public)
		if claim.PublicHost != "" {
			_, _ = fmt.Fprintf(out, "  %s %s\n", styles.label("paths   "), terminalEscapeString(formatExposePaths(claim.PublicPaths)))
		}
		process := fmt.Sprintf("pid %d | %s", claim.OwnerPID, humanDuration(now.Sub(claim.RegisteredAt)))
		_, _ = fmt.Fprintf(out, "  %s %s\n", styles.label("process "), styles.muted(process))
		_, _ = fmt.Fprintf(out, "  %s %s\n", styles.label("cwd     "), styles.muted(terminalEscapeString(shortenHome(claim.OwnerCWD))))
	}
	return nil
}

func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	prefix := home + string(os.PathSeparator)
	if strings.HasPrefix(path, prefix) {
		return "~/" + strings.TrimPrefix(path, prefix)
	}
	return path
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
