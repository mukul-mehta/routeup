package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	_, _ = fmt.Fprintf(out, "current: %s\n", current)
	_, _ = fmt.Fprintf(out, "latest:  %s\n", latest)

	if current == "0.0.0-dev" {
		_, _ = fmt.Fprintln(out, "\nthis is a dev build (built from source); not updating.")
		return nil
	}

	newer, err := update.IsNewer(current, latest)
	if err != nil {
		return fmt.Errorf("comparing versions: %w", err)
	}
	if !newer {
		_, _ = fmt.Fprintln(out, "\nalready up to date.")
		return nil
	}
	if checkOnly {
		_, _ = fmt.Fprintf(out, "\na newer version is available: %s — run `routeup update` to install.\n", latest)
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
		_, _ = fmt.Fprintln(out, "\ninstalled via Homebrew — upgrading with brew...")
		return brewUpgrade(ctx, out)
	}

	_, _ = fmt.Fprintf(out, "\nupdating %s ...\n", resolved)
	if err := update.Apply(ctx, updateRepo, latest, resolved); err != nil {
		return fmt.Errorf("applying update: %w", err)
	}
	_, _ = fmt.Fprintf(out, "updated to %s\n", latest)
	reapplyBind(cmd, out, resolved)
	return nil
}

// reapplyBind re-grants cap_net_bind_service after the binary swap on Linux
// (the capability is on the old inode). No-op on macOS and for high ports.
func reapplyBind(cmd *cobra.Command, out io.Writer, binaryPath string) {
	if runtime.GOOS != "linux" {
		return
	}
	port := state.TLSPortOrDefault()
	if !privbind.Required(port) {
		return
	}
	_, _ = fmt.Fprintln(out, "re-granting port 443 (setcap; asks for your password)...")

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if err := privbind.ReapplyBind(ctx, port, binaryPath); err != nil {
		_, _ = fmt.Fprintf(out, "warning: couldn't reapply setcap: %v\n", err)
		_, _ = fmt.Fprintln(out, "  rerun `routeup setup` to restore port 443")
		return
	}
	_, _ = fmt.Fprintf(out, "port %d: ready\n", port)
}

// brewUpgrade runs `brew upgrade <formula>`, or prints the command if brew
// isn't on PATH.
func brewUpgrade(ctx context.Context, out io.Writer) error {
	brew, err := exec.LookPath("brew")
	if err != nil {
		_, _ = fmt.Fprintf(out, "brew not found; run: brew upgrade %s\n", brewFormula)
		return nil
	}
	c := exec.CommandContext(ctx, brew, "upgrade", brewFormula)
	c.Stdout, c.Stderr = out, out
	return c.Run()
}
