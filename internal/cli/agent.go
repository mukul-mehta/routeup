package cli

import (
	"context"
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
	"github.com/mukul-mehta/routeup/internal/state"
)

// newAgentCmd builds the `routeup agent` command tree.
//
// The local agent starts automatically whenever a command needs it, so these
// subcommands are debugging aids, not part of the normal flow. They are listed
// in --help (unlike the daemon entrypoint `agent run`, which is hidden) so a
// user who needs to inspect or recycle the agent can discover them.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inspect or control the local agent (rarely needed)",
		Long: "Inspect or control the local routeup agent.\n\n" +
			"You normally never run these by hand: the agent starts on demand when\n" +
			"you run `routeup serve` and persists in the background. These commands\n" +
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

// newAgentClient builds an agent client pointed at the resolved socket path,
// carrying this CLI's version so staleness checks work.
func newAgentClient(cmd *cobra.Command) (*agentctl.Client, error) {
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return nil, err
	}
	return agentctl.NewClient(sockPath, "", cmd.Root().Version), nil
}

// newAgentRunCmd is the hidden daemon entrypoint. The CLI re-execs this with
// `agent run` when it needs to spawn an agent; users should not run it
// directly.
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

			a, err := agent.New(agent.Options{
				SocketPath: sockPath,
				ProxyAddr:  ipc.DefaultProxyAddr,
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
	return &cobra.Command{
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
			if err != nil {
				_, _ = fmt.Fprintln(out, "agent: not running")
				return nil
			}

			_, _ = fmt.Fprintln(out, "agent:   running")
			_, _ = fmt.Fprintf(out, "version: %s\n", status.Version)
			_, _ = fmt.Fprintf(out, "uptime:  %ds\n", status.UptimeSeconds)
			_, _ = fmt.Fprintf(out, "proxy:   %s\n", status.ProxyAddr)
			if status.ExecPath != "" {
				_, _ = fmt.Fprintf(out, "binary:  %s\n", status.ExecPath)
			}
			if stale, reason := client.IsStale(status); stale {
				_, _ = fmt.Fprintf(out, "\nnote: %s\n", reason)
				_, _ = fmt.Fprintln(out, "      run `routeup agent restart` to reload.")
			}
			return nil
		},
	}
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
			switch res {
			case agentctl.EnsureAlreadyRunning:
				_, _ = fmt.Fprintln(out, "agent already running")
			case agentctl.EnsureStarted:
				_, _ = fmt.Fprintln(out, "agent started")
			case agentctl.EnsureRestarted:
				_, _ = fmt.Fprintln(out, "agent restarted (build changed)")
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
				_, _ = fmt.Fprintln(out, "agent stopped")
			} else {
				_, _ = fmt.Fprintln(out, "agent not running")
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
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "agent restarted")
			return nil
		},
	}
}
