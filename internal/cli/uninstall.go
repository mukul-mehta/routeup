package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
)

func newUninstallCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Undo setup: remove the certificate, port 443 helper, and local state",
		Long: "Undo what `routeup setup` did on this machine:\n\n" +
			"  - stop the background agent\n" +
			"  - remove the port 443 helper\n" +
			"  - remove routeup's certificate from your trust store\n" +
			"  - delete ~/.routeup\n\n" +
			"Run this BEFORE removing the routeup binary — it needs the binary\n" +
			"to undo the system changes. You'll be asked for your password to\n" +
			"undo the privileged bits.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd, yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runUninstall(cmd *cobra.Command, yes bool) error {
	out := cmd.OutOrStdout()

	if !yes && !confirm(cmd, out) {
		_, _ = fmt.Fprintln(out, "cancelled.")
		return nil
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	stopAgent(cmd, out)

	if err := privbind.Uninstall(ctx); err != nil {
		_, _ = fmt.Fprintf(out, "port helper: couldn't remove (%v)\n", err)
	} else {
		_, _ = fmt.Fprintln(out, "port helper: removed")
	}

	if certPath, err := state.CACertPath(); err == nil {
		if err := certs.UninstallTrust(ctx, certPath); err != nil {
			_, _ = fmt.Fprintf(out, "certificate: couldn't remove from trust store (%v)\n", err)
		} else {
			_, _ = fmt.Fprintln(out, "certificate: removed from trust store")
		}
	}

	if dir, err := state.Dir(); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			_, _ = fmt.Fprintf(out, "state: couldn't delete %s (%v)\n", dir, err)
		} else {
			_, _ = fmt.Fprintf(out, "state: deleted %s\n", dir)
		}
	}

	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "done. you can now remove the routeup binary (e.g. `brew uninstall routeup`).")
	return nil
}

// stopAgent shuts the agent down if it's running. Best-effort.
func stopAgent(cmd *cobra.Command, out io.Writer) {
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return
	}
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)
	stopped, err := client.Stop(ctx)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(out, "agent: couldn't stop (%v)\n", err)
	case stopped:
		_, _ = fmt.Fprintln(out, "agent: stopped")
	default:
		_, _ = fmt.Fprintln(out, "agent: not running")
	}
}

// confirm prompts on out and reads a yes/no answer from the command's input.
func confirm(cmd *cobra.Command, out io.Writer) bool {
	_, _ = fmt.Fprint(out, "This removes routeup's certificate, the port 443 helper, and ~/.routeup. Continue? [y/N] ")
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
