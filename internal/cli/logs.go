// `routeup logs` is a read-only view of an already-running agent:
//
//	finite:  CLI -> GET /v1/logs -> JSON snapshot -> formatted rows
//	follow:  CLI -> GET /v1/logs?follow=true -> SSE -> formatted rows
//
// The command never starts an agent because its in-memory request ring exists
// only for the lifetime of that process. --json writes one Entry per line for
// scripts; normal output keeps IDs short and copyable for `routeup inspect`.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/state"
)

type logsOpts struct {
	follow bool
	public bool
	local  bool
	json   bool
}

// newLogsCmd reads the in-memory log from an already-running agent. Like
// `routeup routes`, it never starts an agent: no agent means no retained logs.
// With --follow, the command holds one SSE connection open until cancellation.
func newLogsCmd() *cobra.Command {
	var opts logsOpts

	cmd := &cobra.Command{
		Use:     "logs [route]",
		Short:   "Show recent local and public route requests",
		Example: "  routeup logs api.myapp\n  routeup logs api.myapp --public --follow\n  routeup logs --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.public && opts.local {
				return errors.New("--public and --local cannot be used together")
			}

			logOpts := logs.ListOptions{}
			if len(args) == 1 {
				logOpts.Route = args[0]
			}
			if opts.public {
				logOpts.Source = logs.SourcePublic
			}
			if opts.local {
				logOpts.Source = logs.SourceLocal
			}
			return runLogs(cmd, logOpts, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.follow, "follow", false, "stream new matching requests")
	cmd.Flags().BoolVar(&opts.public, "public", false, "show only public tunnel requests")
	cmd.Flags().BoolVar(&opts.local, "local", false, "show only local .localhost requests")
	cmd.Flags().BoolVar(&opts.json, "json", false, "write one JSON request record per line")
	return cmd
}

func runLogs(cmd *cobra.Command, opts logs.ListOptions, commandOpts logsOpts) error {
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(socketPath, "", cmd.Root().Version)
	out := cmd.OutOrStdout()

	if commandOpts.follow {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		wroteHeader := false
		err := client.FollowLogs(ctx, opts, func(entry logs.Entry) error {
			if !commandOpts.json && !wroteHeader {
				if err := writeLogHeader(out); err != nil {
					return err
				}
				wroteHeader = true
			}
			return writeLogEntry(out, entry, commandOpts.json)
		})
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		if err != nil {
			_, _ = fmt.Fprintln(out, "no request logs (agent not running)")
			return nil
		}
		return nil
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	entries, err := client.Logs(ctx, opts)
	if err != nil {
		_, _ = fmt.Fprintln(out, "no request logs (agent not running)")
		return nil
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "no matching request logs")
		return nil
	}
	if !commandOpts.json {
		if err := writeLogHeader(out); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if err := writeLogEntry(out, entry, commandOpts.json); err != nil {
			return err
		}
	}
	return nil
}

func writeLogHeader(out io.Writer) error {
	_, err := fmt.Fprintln(out, "TIME     SOURCE ROUTE                METHOD  PATH STATUS DURATION ID")
	if err != nil {
		return fmt.Errorf("write request log header: %w", err)
	}
	return nil
}

func writeLogEntry(out io.Writer, entry logs.Entry, jsonOutput bool) error {
	if jsonOutput {
		if err := json.NewEncoder(out).Encode(entry); err != nil {
			return fmt.Errorf("write request log json: %w", err)
		}
		return nil
	}
	_, err := fmt.Fprintf(out, "%s %-6s %-20s %-7s %s %d %s %s\n",
		entry.StartedAt.Local().Format("15:04:05"), entry.Source, entry.Route,
		entry.Method, entry.RequestPath, entry.Status, formatLogDuration(entry.Duration), entry.ID)
	if err != nil {
		return fmt.Errorf("write request log: %w", err)
	}
	return nil
}

func formatLogDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "0ms"
	}
	return duration.Round(time.Millisecond).String()
}
