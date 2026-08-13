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
	"errors"
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
	if agentctl.IsUnavailable(err) {
		return errors.New("agent not running; retained requests are unavailable")
	}
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
	return writeInspectEntryStyled(out, entry, raw, newTerminalStyles(out))
}

func writeInspectEntryStyled(out io.Writer, entry logs.Entry, raw bool, styles terminalStyles) error {
	if entry.Capture == nil {
		return fmt.Errorf("request %s was not captured", terminalEscapeString(entry.ID))
	}

	if _, err := fmt.Fprintf(out, "%s %s\n\n%s\n--------\n%s %s\n%s %s\n%s %s:%d\n%s %s\n%s %s\n%s %s\n%s %s\n%s %s\n",
		styles.label("Request"), styles.accent(terminalEscapeString(entry.ID)), styles.label("Metadata"), styles.label("Source:"), terminalEscapeString(string(entry.Source)),
		styles.label("Route:"), terminalEscapeString(entry.Route), styles.label("Target:"), terminalEscapeString(entry.Target.Path), entry.Target.Port,
		styles.label("Status:"), styles.statusCode(entry.Status), styles.label("Duration:"), formatLogDuration(entry.Duration),
		styles.label("Method:"), terminalEscapeString(entry.Method), styles.label("Path:"), terminalEscapeString(entry.RequestPath),
		styles.label("Host:"), terminalEscapeString(entry.Host)); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	if err := writeCapturedMessageStyled(out, "Request", entry.Capture.Request, raw, styles); err != nil {
		return err
	}
	return writeCapturedMessageStyled(out, "Response", entry.Capture.Response, raw, styles)
}

func writeCapturedMessageStyled(out io.Writer, label string, msg *logs.CapturedMessage, raw bool, styles terminalStyles) error {
	if _, err := fmt.Fprintf(out, "\n%s\n%s\n", styles.label(label), strings.Repeat("-", len(label))); err != nil {
		return fmt.Errorf("write %s label: %w", label, err)
	}
	if msg == nil {
		if _, err := fmt.Fprintln(out, "<not captured>"); err != nil {
			return fmt.Errorf("write %s not-captured: %w", label, err)
		}
		return nil
	}
	status := captureStatus(msg.Complete)
	if msg.Complete {
		status = styles.success(status)
	} else {
		status = styles.warning(status)
	}
	if _, err := fmt.Fprintf(out, "%s %s\n%s %d\n", styles.label("Capture:"), status, styles.label("Body bytes:"), len(msg.Body)); err != nil {
		return fmt.Errorf("write %s capture status: %w", label, err)
	}
	if len(msg.RedactedHeaders) > 0 {
		redacted := make([]string, len(msg.RedactedHeaders))
		for i, name := range msg.RedactedHeaders {
			redacted[i] = terminalEscapeString(name)
		}
		if _, err := fmt.Fprintf(out, "%s %s\n", styles.label("Redacted:"), styles.warning(strings.Join(redacted, ", "))); err != nil {
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
	if _, err := fmt.Fprintf(out, "\n%s\n----\n", styles.label("Body")); err != nil {
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
