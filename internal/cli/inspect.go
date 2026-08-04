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

	request := entry.Capture.Request
	if _, err := fmt.Fprintf(out, "Request %s\n\nMetadata\n--------\nSource: %s\nRoute: %s\nTarget: %s:%d\nStatus: %d\nDuration: %s\nCapture: %s\nBody bytes: %d\n",
		entry.ID, entry.Source, entry.Route, entry.Target.Path, entry.Target.Port, entry.Status,
		formatLogDuration(entry.Duration), captureStatus(request.Complete), len(request.Body)); err != nil {
		return fmt.Errorf("write request summary: %w", err)
	}
	if _, err := fmt.Fprintf(out, "Method: %s\nPath: %s\nHost: %s\n\nHeaders\n-------\n", entry.Method, entry.RequestPath, entry.Host); err != nil {
		return fmt.Errorf("write request line: %w", err)
	}
	if len(request.RedactedHeaders) > 0 {
		if _, err := fmt.Fprintf(out, "Redacted: %s\n", strings.Join(request.RedactedHeaders, ", ")); err != nil {
			return fmt.Errorf("write redacted headers: %w", err)
		}
	}
	if len(request.Headers) == 0 {
		if _, err := fmt.Fprintln(out, "<none>"); err != nil {
			return fmt.Errorf("write request headers: %w", err)
		}
	} else if err := request.Headers.Write(out); err != nil {
		return fmt.Errorf("write request headers: %w", err)
	}
	if _, err := fmt.Fprintln(out, "\nBody\n----"); err != nil {
		return fmt.Errorf("write request body label: %w", err)
	}
	if len(request.Body) == 0 {
		if _, err := fmt.Fprintln(out, "<empty>"); err != nil {
			return fmt.Errorf("write empty request body: %w", err)
		}
		return nil
	}
	if _, err := out.Write(request.Body); err != nil {
		return fmt.Errorf("write request body: %w", err)
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return fmt.Errorf("finish request body: %w", err)
	}
	return nil
}

func captureStatus(complete bool) string {
	if complete {
		return "complete"
	}
	return "partial"
}
