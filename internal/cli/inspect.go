// `routeup inspect` reads the original request retained by an opted-in route:
//
//	CLI -> agentctl.Inspect -> GET /v1/requests/{id} -> log ring
//
// It never starts an agent because retained requests are process-local. The
// command prints only data returned over the per-user Unix socket.
package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/state"
)

func newInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "inspect <request-id>",
		Short:   "Show an opted-in captured request",
		Example: "routeup inspect req_Ap7kQ3mN8vR2xLzC",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd, args[0])
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
			entries, err := c.Logs(ctx, logs.ListOptions{})
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var ids []string
			for _, e := range entries {
				if e.Capture != nil {
					ids = append(ids, e.ID)
				}
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
	}
}

func runInspect(cmd *cobra.Command, id string) error {
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(socketPath, "", cmd.Root().Version)
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	entry, err := client.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return writeInspectEntry(cmd.OutOrStdout(), entry)
}

func writeInspectEntry(out io.Writer, entry logs.Entry) error {
	if entry.Capture == nil {
		return fmt.Errorf("request %s was not captured", entry.ID)
	}

	if _, err := fmt.Fprintf(out, "Request %s\n\nMetadata\n--------\nSource: %s\nRoute: %s\nTarget: %s:%d\nStatus: %d\nDuration: %s\nMethod: %s\nPath: %s\nHost: %s\n",
		entry.ID, entry.Source, entry.Route, entry.Target.Path, entry.Target.Port, entry.Status,
		formatLogDuration(entry.Duration), entry.Method, entry.RequestPath, entry.Host); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	if err := writeCapturedMessage(out, "Request", entry.Capture.Request); err != nil {
		return err
	}
	return writeCapturedMessage(out, "Response", entry.Capture.Response)
}

func writeCapturedMessage(out io.Writer, label string, msg logs.CapturedMessage) error {
	if _, err := fmt.Fprintf(out, "\n%s\n%s\n", label, strings.Repeat("-", len(label))); err != nil {
		return fmt.Errorf("write %s label: %w", label, err)
	}
	if msg.Headers == nil {
		if _, err := fmt.Fprintln(out, "<not captured>"); err != nil {
			return fmt.Errorf("write %s not-captured: %w", label, err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(out, "Capture: %s\nBody bytes: %d\n", captureStatus(msg.Complete), len(msg.Body)); err != nil {
		return fmt.Errorf("write %s capture status: %w", label, err)
	}
	if len(msg.RedactedHeaders) > 0 {
		if _, err := fmt.Fprintf(out, "Redacted: %s\n", strings.Join(msg.RedactedHeaders, ", ")); err != nil {
			return fmt.Errorf("write %s redacted headers: %w", label, err)
		}
	}
	if len(msg.Headers) == 0 {
		if _, err := fmt.Fprintln(out, "<no headers>"); err != nil {
			return fmt.Errorf("write %s empty headers: %w", label, err)
		}
	} else if err := msg.Headers.Write(out); err != nil {
		return fmt.Errorf("write %s headers: %w", label, err)
	}
	if _, err := fmt.Fprintln(out, "\nBody\n----"); err != nil {
		return fmt.Errorf("write %s body label: %w", label, err)
	}
	if len(msg.Body) == 0 {
		if _, err := fmt.Fprintln(out, "<empty>"); err != nil {
			return fmt.Errorf("write %s empty body: %w", label, err)
		}
		return nil
	}
	if _, err := out.Write(msg.Body); err != nil {
		return fmt.Errorf("write %s body: %w", label, err)
	}
	_, err := fmt.Fprintln(out)
	return err
}

func captureStatus(complete bool) string {
	if complete {
		return "complete"
	}
	return "partial"
}
