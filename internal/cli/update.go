package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
	"github.com/mukul-mehta/routeup/internal/update"
)

const (
	updateRepo  = "mukul-mehta/routeup"
	brewFormula = "mukul-mehta/tap/routeup"
)

func newUpdateCmd() *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update routeup to the latest release",
		Long: "Check for a newer routeup release and install it.\n\n" +
			"Homebrew installs are upgraded with `brew upgrade`; direct installs\n" +
			"(the curl installer) replace the binary in place after verifying its\n" +
			"checksum.\n\n" +
			"routeup never checks for updates on its own — this command is the\n" +
			"only thing that contacts GitHub.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report the latest version without installing")
	return cmd
}

func runUpdate(cmd *cobra.Command, checkOnly bool) error {
	out := cmd.OutOrStdout()
	styles := newTerminalStyles(out)
	current := cmd.Root().Version

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	latest, err := update.Latest(ctx, updateRepo)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("current:"), terminalEscapeString(current))
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("latest: "), styles.accent(terminalEscapeString(latest)))

	if current == "0.0.0-dev" || strings.HasPrefix(current, "0.0.0-devel+") {
		_, _ = fmt.Fprintln(out, "\n"+styles.muted("this is a development build; not updating."))
		return nil
	}

	newer, err := update.IsNewer(current, latest)
	if err != nil {
		return fmt.Errorf("comparing versions: %w", err)
	}
	if !newer {
		_, _ = fmt.Fprintln(out, "\n"+styles.success("already up to date."))
		return nil
	}
	if checkOnly {
		_, _ = fmt.Fprintf(out, "\n%s %s\n", styles.warning("newer version available:"), styles.accent(latest+" - run `routeup update` to install"))
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}

	if update.DetectChannel(resolved) == update.ChannelHomebrew {
		_, _ = fmt.Fprintln(out, "\n"+styles.warning("installed via Homebrew - upgrading with brew..."))
		if err := brewUpgrade(ctx, out); err != nil {
			return err
		}
		reapplyBind(cmd, out, exe)
		return nil
	}

	_, _ = fmt.Fprintf(out, "\n%s %s\n", styles.warning("updating"), styles.muted(terminalEscapeString(resolved)))
	if err := update.Apply(ctx, updateRepo, latest, resolved); err != nil {
		return fmt.Errorf("applying update: %w", err)
	}
	_, _ = fmt.Fprintln(out, styles.success("updated to "+terminalEscapeString(latest)))
	reapplyBind(cmd, out, resolved)
	return nil
}

// reapplyBind refreshes platform-specific privileged-port setup after an update.
func reapplyBind(cmd *cobra.Command, out io.Writer, binaryPath string) {
	styles := newTerminalStyles(out)
	port := state.TLSPortOrDefault()
	if !privbind.Required(port) {
		return
	}
	_, _ = fmt.Fprintln(out, styles.warning(fmt.Sprintf("refreshing privileged port %d setup (asks for your password)...", port)))

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if err := privbind.ReapplyBind(ctx, port, binaryPath); err != nil {
		_, _ = fmt.Fprintln(out, styles.warning(fmt.Sprintf("warning: couldn't refresh privileged port setup: %v", err)))
		_, _ = fmt.Fprintln(out, styles.muted(fmt.Sprintf("  rerun `routeup setup` to restore port %d", port)))
		return
	}
	_, _ = fmt.Fprintln(out, styles.success(fmt.Sprintf("port %d: ready", port)))
}

// brewUpgrade runs `brew upgrade <formula>`, or prints the command if brew
// isn't on PATH.
func brewUpgrade(ctx context.Context, out io.Writer) error {
	brew, err := exec.LookPath("brew")
	if err != nil {
		styles := newTerminalStyles(out)
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.warning("brew not found; run:"), styles.accent("brew upgrade "+brewFormula))
		return nil
	}
	c := exec.CommandContext(ctx, brew, "upgrade", brewFormula)
	c.Stdout, c.Stderr = out, out
	return c.Run()
}
