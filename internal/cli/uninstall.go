package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	styles := newTerminalStyles(out)

	if !yes && !confirm(cmd, out) {
		_, _ = fmt.Fprintln(out, styles.muted("cancelled."))
		return nil
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	isolatedDir := ""
	if state.IsDirOverridden() {
		var err error
		isolatedDir, err = safeIsolatedStateDir()
		if err != nil {
			return err
		}
	}

	if err := stopOwnersAndAgent(cmd, out); err != nil {
		return err
	}
	if isolatedDir != "" {
		certPath := filepath.Join(isolatedDir, state.CACertName)
		if _, err := os.Stat(certPath); err == nil {
			if err := certs.UninstallTrust(ctx, certPath); err != nil {
				return fmt.Errorf("remove isolated CA from trust store: %w", err)
			}
			_, _ = fmt.Fprintln(out, styles.success("isolated certificate: removed from trust store"))
		}
		retained, err := removeIsolatedState(isolatedDir)
		if err != nil {
			return err
		}
		if retained {
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.success("isolated state files: deleted"), styles.muted("(kept non-routeup files in "+terminalEscapeString(isolatedDir)+")"))
		} else {
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.success("isolated state: deleted"), styles.muted(terminalEscapeString(isolatedDir)))
		}
		return nil
	}

	if err := privbind.Uninstall(ctx); err != nil {
		_, _ = fmt.Fprintln(out, styles.warning(fmt.Sprintf("port helper: couldn't remove (%v)", err)))
	} else {
		_, _ = fmt.Fprintln(out, styles.success("port helper: removed"))
	}

	if certPath, err := state.CACertPath(); err == nil {
		if err := certs.UninstallTrust(ctx, certPath); err != nil {
			_, _ = fmt.Fprintln(out, styles.warning(fmt.Sprintf("certificate: couldn't remove from trust store (%v)", err)))
		} else {
			_, _ = fmt.Fprintln(out, styles.success("certificate: removed from trust store"))
		}
	}

	if dir, err := state.Dir(); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			_, _ = fmt.Fprintln(out, styles.warning(fmt.Sprintf("state: couldn't delete %s (%v)", terminalEscapeString(dir), err)))
		} else {
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.success("state: deleted"), styles.muted(terminalEscapeString(dir)))
		}
	}

	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, styles.success("done. you can now remove the routeup binary (e.g. `brew uninstall routeup`)."))
	return nil
}

func stopOwnersAndAgent(cmd *cobra.Command, out io.Writer) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := stopActiveOwners(cmd, out); err != nil {
			return err
		}
		if err := stopAgent(cmd, out); err != nil {
			return err
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-cmd.Context().Done():
			timer.Stop()
			return cmd.Context().Err()
		case <-timer.C:
		}
		owners, err := state.LiveOwners()
		if err != nil {
			return fmt.Errorf("recheck active routeup owners: %w", err)
		}
		if len(owners) == 0 {
			return nil
		}
	}
	return errors.New("routeup owners kept starting during uninstall; stop them and retry")
}

func stopActiveOwners(cmd *cobra.Command, out io.Writer) error {
	owners, err := state.LiveOwners()
	if err != nil {
		return fmt.Errorf("read active routeup owners: %w", err)
	}
	if len(owners) == 0 {
		return nil
	}
	for _, owner := range owners {
		if owner.Kind != state.OwnerServe {
			return fmt.Errorf("cannot uninstall while %s owner for route %q is active (pid %d); stop its command first", owner.Kind, owner.Route, owner.PID)
		}
	}
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 85*time.Second)
	defer cancel()
	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)
	stopped := 0
	stoppedRoutes := make(map[string]struct{})
	for _, owner := range owners {
		if _, ok := stoppedRoutes[owner.Route]; ok {
			continue
		}
		found, stopErr := stopRouteAfterReconcile(ctx, client, owner.Route, 75*time.Second)
		if stopErr != nil {
			return fmt.Errorf("stop route %q before uninstall: %w", owner.Route, stopErr)
		}
		if !found {
			return fmt.Errorf("cannot uninstall while serve owner for route %q is active (pid %d)", owner.Route, owner.PID)
		}
		if err := waitForRouteStop(ctx, client, owner.Route); err != nil {
			return fmt.Errorf("stop route %q before uninstall: %w", owner.Route, err)
		}
		stoppedRoutes[owner.Route] = struct{}{}
		stopped++
	}
	if stopped > 0 {
		_, _ = fmt.Fprintf(out, "%s %d\n", newTerminalStyles(out).success("route owners: stopped"), stopped)
	}
	return waitForOwnerRecordsGone(ctx)
}

func waitForOwnerRecordsGone(ctx context.Context) error {
	for {
		remaining, err := state.LiveOwners()
		if err != nil {
			return fmt.Errorf("verify routeup owners stopped: %w", err)
		}
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			owner := remaining[0]
			return fmt.Errorf("cannot uninstall while %s owner for route %q is active (pid %d): %w", owner.Kind, owner.Route, owner.PID, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func safeIsolatedStateDir() (string, error) {
	dir, err := state.Dir()
	if err != nil {
		return "", err
	}
	protected := make([]string, 0, 2)
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		protected = append(protected, home)
	}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		protected = append(protected, cwd)
	}
	for _, path := range protected {
		contains, relErr := pathContains(dir, path)
		if relErr != nil {
			return "", fmt.Errorf("compare isolated state path: %w", relErr)
		}
		if contains {
			return "", fmt.Errorf("refusing to remove unsafe isolated state directory %s", dir)
		}
	}
	markerPath := filepath.Join(dir, state.SetupMarkerName)
	info, err := os.Lstat(markerPath)
	if err != nil {
		return "", fmt.Errorf("refusing to remove unrecognized isolated state directory %s: %w", dir, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing to remove unrecognized isolated state directory %s", dir)
	}
	if _, err := state.ReadSetupMarker(); err != nil {
		return "", fmt.Errorf("refusing to remove isolated state with unreadable setup marker: %w", err)
	}
	return dir, nil
}

func pathContains(parent, child string) (bool, error) {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))), nil
}

func removeIsolatedState(dir string) (bool, error) {
	owned := []string{
		state.AgentSocketName,
		state.AgentLogName,
		state.AgentPIDName,
		state.OwnersDirName,
		state.CACertName,
		state.CAKeyName,
		state.ClientConfigName,
		state.SetupMarkerName,
	}
	for _, name := range owned {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return false, fmt.Errorf("delete isolated state file %s: %w", name, err)
		}
	}
	if err := os.Remove(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return true, nil
		}
		return false, fmt.Errorf("remove isolated state directory %s: %w", dir, err)
	}
	return false, nil
}

// stopAgent shuts the agent down if it's running. Best-effort.
func stopAgent(cmd *cobra.Command, out io.Writer) error {
	styles := newTerminalStyles(out)
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return err
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
		return fmt.Errorf("stop agent before uninstall: %w", err)
	case stopped:
		_, _ = fmt.Fprintln(out, styles.success("agent: stopped"))
	default:
		_, _ = fmt.Fprintln(out, styles.muted("agent: not running"))
	}
	return nil
}

// confirm prompts on out and reads a yes/no answer from the command's input.
func confirm(cmd *cobra.Command, out io.Writer) bool {
	styles := newTerminalStyles(out)
	if state.IsDirOverridden() {
		dir, _ := state.Dir()
		_, _ = fmt.Fprintf(out, "%s %s ", styles.warning("This removes isolated routeup state at "+terminalEscapeString(dir)+". Continue?"), styles.label("[y/N]"))
	} else {
		_, _ = fmt.Fprintf(out, "%s %s ", styles.warning("This removes routeup's certificate, the port 443 helper, and ~/.routeup. Continue?"), styles.label("[y/N]"))
	}
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
