package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
)

func installPrivBind(cmd *cobra.Command, out io.Writer, userPort int) error {
	styles := newTerminalStyles(out)
	if !privbind.Required(userPort) {
		_, _ = fmt.Fprintln(out, styles.success(fmt.Sprintf("port %d: ready", userPort)))
		return nil
	}
	_, _ = fmt.Fprintln(out, styles.warning(fmt.Sprintf("setting up port %d (asks for your password)...", userPort)))

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if err := privbind.Install(ctx, userPort); err != nil {
		return fmt.Errorf("setting up port %d: %w", userPort, err)
	}
	_, _ = fmt.Fprintln(out, styles.success(fmt.Sprintf("port %d: ready", userPort)))
	return nil
}

func installCATrust(cmd *cobra.Command, out io.Writer, certPath string, useSystem bool) error {
	styles := newTerminalStyles(out)
	if useSystem {
		_, _ = fmt.Fprintln(out, styles.warning("trusting the certificate system-wide (asks for your password)..."))
	} else {
		_, _ = fmt.Fprintln(out, styles.warning("trusting the certificate..."))
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if err := certs.InstallTrust(ctx, certPath, certs.TrustOptions{System: useSystem}); err != nil {
		return fmt.Errorf("trusting certificate: %w", err)
	}
	_, _ = fmt.Fprintln(out, styles.success("certificate: trusted"))
	return nil
}

func ensureCATrust(cmd *cobra.Command, out io.Writer, certPath string, useSystem bool) error {
	trusted, err := certs.VerifyTrust(certPath)
	if err == nil && trusted {
		_, _ = fmt.Fprintln(out, newTerminalStyles(out).success("certificate: already trusted"))
		return nil
	}
	return installCATrust(cmd, out, certPath, useSystem)
}

func startLocalAgent(cmd *cobra.Command, out io.Writer) error {
	styles := newTerminalStyles(out)
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return fmt.Errorf("resolve agent socket path: %w", err)
	}
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	startCtx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)
	res, err := client.EnsureRunning(startCtx)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	switch res {
	case agentctl.EnsureAlreadyRunning:
		_, _ = fmt.Fprintln(out, styles.success("agent: already running"))
	case agentctl.EnsureStarted:
		_, _ = fmt.Fprintln(out, styles.success("agent: started"))
	case agentctl.EnsureRestarted:
		_, _ = fmt.Fprintln(out, styles.success("agent: restarted (build changed)"))
	}
	return nil
}
