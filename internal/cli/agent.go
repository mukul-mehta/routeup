package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agent"
	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
)

// newAgentCmd builds the `routeup agent` debug tree. The agent normally
// starts on demand; these are for inspect/restart.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inspect or control the local agent (rarely needed)",
		Long: "Inspect or control the local routeup agent.\n\n" +
			"You normally never run these by hand: the agent starts on demand when\n" +
			"you run bare `routeup` or `routeup serve`, and persists in the background.\n" +
			"These commands\n" +
			"exist for debugging and for forcing a reload after upgrading routeup.",
	}
	cmd.AddCommand(
		newAgentRunCmd(),
		newAgentStatusCmd(),
		newAgentStartCmd(),
		newAgentStopCmd(),
		newAgentRestartCmd(),
	)
	return cmd
}

// newAgentClient builds an agent client carrying this CLI's version.
func newAgentClient(cmd *cobra.Command) (*agentctl.Client, error) {
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return nil, err
	}
	return agentctl.NewClient(sockPath, "", cmd.Root().Version), nil
}

// newAgentRunCmd is the hidden daemon entrypoint.
func newAgentRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "(internal) run the agent in the foreground",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sockPath, err := state.AgentSocketPath()
			if err != nil {
				return err
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))

			tlsPort := state.TLSPortOrDefault()
			bindPort := privbind.AgentBindPort(tlsPort)
			tlsAddr := fmt.Sprintf("127.0.0.1:%d", bindPort)

			a, err := agent.New(agent.Options{
				SocketPath: sockPath,
				TLSAddr:    tlsAddr,
				Version:    cmd.Root().Version,
				Logger:     logger,
			})
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer stop()

			return a.Run(ctx)
		},
	}
}

func newAgentStatusCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the agent is running and its build",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAgentClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()

			out := cmd.OutOrStdout()
			status, err := client.Status(ctx)
			if agentctl.IsUnavailable(err) {
				if jsonOutput {
					return json.NewEncoder(out).Encode(map[string]bool{"running": false})
				}
				_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("agent: not running"))
				return nil
			}
			if err != nil {
				return fmt.Errorf("get agent status: %w", err)
			}
			if jsonOutput {
				if err := json.NewEncoder(out).Encode(struct {
					Running bool       `json:"running"`
					Status  ipc.Status `json:"status"`
				}{Running: true, Status: status}); err != nil {
					return fmt.Errorf("write agent status json: %w", err)
				}
				return nil
			}

			styles := newTerminalStyles(out)
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("agent:  "), styles.success("running"))
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("version:"), status.Version)
			_, _ = fmt.Fprintf(out, "%s %ds\n", styles.label("uptime: "), status.UptimeSeconds)
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("tls:    "), status.TLSAddr)
			if status.ExecPath != "" {
				_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("binary: "), terminalEscapeString(status.ExecPath))
			}
			if len(status.Exposures) != 0 {
				_, _ = fmt.Fprintln(out, "")
				_, _ = fmt.Fprintln(out, styles.label("public exposures:"))
				for _, exposure := range status.Exposures {
					stateText := fmt.Sprintf("%-12s", exposure.State)
					state := styles.success(stateText)
					if exposure.State == ipc.ExposureReconnecting {
						state = styles.warning(stateText)
					}
					_, _ = fmt.Fprintf(out, "  %s %s  %s  %s  pid %d\n", state, styles.accent(terminalEscapeString(exposure.Route)),
						styles.url("https://"+terminalEscapeString(exposure.Host)), terminalEscapeString(formatExposePaths(exposure.Paths)), exposure.OwnerPID)
				}
			}
			if stale, reason := client.IsStale(status); stale {
				_, _ = fmt.Fprintf(out, "\n%s %s\n", styles.warning("note:"), reason)
				_, _ = fmt.Fprintln(out, styles.muted("      run `routeup agent restart` to reload."))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write agent status as JSON")
	return cmd
}

func newAgentStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the agent if it is not already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAgentClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			res, err := client.EnsureRunning(ctx)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			styles := newTerminalStyles(out)
			switch res {
			case agentctl.EnsureAlreadyRunning:
				_, _ = fmt.Fprintln(out, styles.success("agent already running"))
			case agentctl.EnsureStarted:
				_, _ = fmt.Fprintln(out, styles.success("agent started"))
			case agentctl.EnsureRestarted:
				_, _ = fmt.Fprintln(out, styles.success("agent restarted (build changed)"))
			}
			return nil
		},
	}
}

func newAgentStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAgentClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			out := cmd.OutOrStdout()
			stopped, err := client.Stop(ctx)
			if err != nil {
				return err
			}
			if stopped {
				_, _ = fmt.Fprintln(out, newTerminalStyles(out).success("agent stopped"))
			} else {
				_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("agent not running"))
			}
			return nil
		},
	}
}

func newAgentRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop the agent if running, then start a fresh one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAgentClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			if err := client.Restart(ctx); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, newTerminalStyles(out).success("agent restarted"))
			return nil
		},
	}
}
