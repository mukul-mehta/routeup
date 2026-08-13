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
	"strings"
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
	plain  bool
	limit  int
	since  string
	method string
	status int
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
			if opts.plain && opts.json {
				return errors.New("--plain and --json cannot be used together")
			}
			if opts.plain && !opts.follow {
				return errors.New("--plain requires --follow")
			}

			logOpts := logs.ListOptions{}
			if opts.limit < 0 {
				return errors.New("--limit cannot be negative")
			}
			if opts.status != 0 && (opts.status < 100 || opts.status > 599) {
				return fmt.Errorf("--status must be between 100 and 599 (got %d)", opts.status)
			}
			since, err := parseLogSince(opts.since, time.Now())
			if err != nil {
				return err
			}
			logOpts.Limit = opts.limit
			logOpts.Since = since
			logOpts.Method = strings.ToUpper(strings.TrimSpace(opts.method))
			logOpts.Status = opts.status
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
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			socketPath, err := state.AgentSocketPath()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			c := agentctl.NewClient(socketPath, "", cmd.Root().Version)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			claims, err := c.List(ctx)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names := make([]string, 0, len(claims))
			for _, claim := range claims {
				names = append(names, claim.Name)
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
	}
	cmd.Flags().BoolVar(&opts.follow, "follow", false, "stream new matching requests")
	cmd.Flags().BoolVar(&opts.public, "public", false, "show only public tunnel requests")
	cmd.Flags().BoolVar(&opts.local, "local", false, "show only local .localhost requests")
	cmd.Flags().BoolVar(&opts.json, "json", false, "write one JSON request record per line")
	cmd.Flags().BoolVar(&opts.plain, "plain", false, "with --follow, stream lines instead of opening the interactive viewer")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "show at most the newest N matching requests")
	cmd.Flags().StringVar(&opts.since, "since", "", "show requests since a duration ago or RFC3339 time")
	cmd.Flags().StringVar(&opts.method, "method", "", "show only this HTTP method")
	cmd.Flags().IntVar(&opts.status, "status", 0, "show only this HTTP status code")
	return cmd
}

func parseLogSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration < 0 {
			return time.Time{}, errors.New("--since duration cannot be negative")
		}
		return now.Add(-duration), nil
	}
	since, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --since %q (use a duration like 10m or an RFC3339 time)", value)
	}
	return since, nil
}

func runLogs(cmd *cobra.Command, opts logs.ListOptions, commandOpts logsOpts) error {
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(socketPath, "", cmd.Root().Version)
	out := cmd.OutOrStdout()

	if commandOpts.follow {
		if !commandOpts.json && !commandOpts.plain && terminalIsInteractive(cmd.InOrStdin(), out) {
			return runLogsTUI(cmd, client, opts)
		}
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
		if agentctl.IsUnavailable(err) {
			if commandOpts.json {
				return nil
			}
			_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("no request logs (agent not running)"))
			return nil
		}
		return err
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	entries, err := client.Logs(ctx, opts)
	if agentctl.IsUnavailable(err) {
		if commandOpts.json {
			return nil
		}
		_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("no request logs (agent not running)"))
		return nil
	}
	if err != nil {
		return fmt.Errorf("list request logs: %w", err)
	}
	if len(entries) == 0 {
		if commandOpts.json {
			return nil
		}
		_, _ = fmt.Fprintln(out, newTerminalStyles(out).muted("no matching request logs"))
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
	styles := newTerminalStyles(out)
	_, err := fmt.Fprintln(out, styles.label("TIME      SOURCE  ROUTE                 METHOD   PATH                                      STATUS  DURATION  ID"))
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
	styles := newTerminalStyles(out)
	source := fmt.Sprintf("%-6s", terminalEscapeString(string(entry.Source)))
	if entry.Source == logs.SourcePublic {
		source = styles.accent(source)
	} else {
		source = styles.muted(source)
	}
	routeName := styles.label(fmt.Sprintf("%-20s", terminalEscapeString(entry.Route)))
	method := styles.label(fmt.Sprintf("%-7s", terminalEscapeString(entry.Method)))
	requestPath := fmt.Sprintf("%-40s", terminalEscapeString(entry.RequestPath))
	duration := styles.muted(fmt.Sprintf("%-8s", formatLogDuration(entry.Duration)))
	status := styles.statusCode(entry.Status) + strings.Repeat(" ", max(0, 6-len(fmt.Sprintf("%d", entry.Status))))
	_, err := fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s\n",
		styles.muted(entry.StartedAt.Local().Format("15:04:05")), source, routeName,
		method, requestPath, status, duration, styles.muted(terminalEscapeString(entry.ID)))
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
