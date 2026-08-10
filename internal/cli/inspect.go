// `routeup inspect` reads the original request retained by an opted-in route:
//
//	CLI -> agentctl.Inspect -> GET /v1/requests/{id} -> log ring
//
// It never starts an agent because retained exchanges are process-local. The
// command prints only data returned over the per-user Unix socket.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/state"
)

type inspectOpts struct {
	raw  bool
	json bool
}

func newInspectCmd() *cobra.Command {
	var opts inspectOpts
	cmd := &cobra.Command{
		Use:     "inspect <request-id>",
		Short:   "Show an opted-in captured request and response",
		Example: "routeup inspect req_Ap7kQ3mN8vR2xLzC",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd, args[0], opts)
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
				if e.Captured {
					ids = append(ids, e.ID)
				}
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
	}
	cmd.Flags().BoolVar(&opts.raw, "raw", false, "write unescaped captured values (unsafe for direct terminal output)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "write the captured exchange as JSON")
	cmd.MarkFlagsMutuallyExclusive("raw", "json")
	return cmd
}

func runInspect(cmd *cobra.Command, id string, opts inspectOpts) error {
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
	if opts.json {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(entry); err != nil {
			return fmt.Errorf("write inspected request json: %w", err)
		}
		return nil
	}
	return writeInspectEntry(cmd.OutOrStdout(), entry, opts.raw)
}

func writeInspectEntry(out io.Writer, entry logs.Entry, raw bool) error {
	if entry.Capture == nil {
		return fmt.Errorf("request %s was not captured", terminalEscapeString(entry.ID))
	}

	if _, err := fmt.Fprintf(out, "Request %s\n\nMetadata\n--------\nSource: %s\nRoute: %s\nTarget: %s:%d\nStatus: %d\nDuration: %s\nMethod: %s\nPath: %s\nHost: %s\n",
		terminalEscapeString(entry.ID), terminalEscapeString(string(entry.Source)), terminalEscapeString(entry.Route),
		terminalEscapeString(entry.Target.Path), entry.Target.Port, entry.Status, formatLogDuration(entry.Duration),
		terminalEscapeString(entry.Method), terminalEscapeString(entry.RequestPath), terminalEscapeString(entry.Host)); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	if err := writeCapturedMessage(out, "Request", entry.Capture.Request, raw); err != nil {
		return err
	}
	return writeCapturedMessage(out, "Response", entry.Capture.Response, raw)
}

func writeCapturedMessage(out io.Writer, label string, msg *logs.CapturedMessage, raw bool) error {
	if _, err := fmt.Fprintf(out, "\n%s\n%s\n", label, strings.Repeat("-", len(label))); err != nil {
		return fmt.Errorf("write %s label: %w", label, err)
	}
	if msg == nil {
		if _, err := fmt.Fprintln(out, "<not captured>"); err != nil {
			return fmt.Errorf("write %s not-captured: %w", label, err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(out, "Capture: %s\nBody bytes: %d\n", captureStatus(msg.Complete), len(msg.Body)); err != nil {
		return fmt.Errorf("write %s capture status: %w", label, err)
	}
	if len(msg.RedactedHeaders) > 0 {
		redacted := make([]string, len(msg.RedactedHeaders))
		for i, name := range msg.RedactedHeaders {
			redacted[i] = terminalEscapeString(name)
		}
		if _, err := fmt.Fprintf(out, "Redacted: %s\n", strings.Join(redacted, ", ")); err != nil {
			return fmt.Errorf("write %s redacted headers: %w", label, err)
		}
	}
	if len(msg.Headers) == 0 {
		if _, err := fmt.Fprintln(out, "<no headers>"); err != nil {
			return fmt.Errorf("write %s empty headers: %w", label, err)
		}
	} else if raw {
		if err := msg.Headers.Write(out); err != nil {
			return fmt.Errorf("write %s headers: %w", label, err)
		}
	} else if err := writeSafeHeaders(out, msg.Headers); err != nil {
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
	body := msg.Body
	if !raw {
		body = []byte(terminalEscapeBytes(body))
	}
	if _, err := out.Write(body); err != nil {
		return fmt.Errorf("write %s body: %w", label, err)
	}
	_, err := fmt.Fprintln(out)
	return err
}

func writeSafeHeaders(out io.Writer, headers map[string][]string) error {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers[key] {
			if _, err := fmt.Fprintf(out, "%s: %s\n", terminalEscapeString(key), terminalEscapeString(value)); err != nil {
				return err
			}
		}
	}
	return nil
}

func captureStatus(complete bool) string {
	if complete {
		return "complete"
	}
	return "partial"
}
