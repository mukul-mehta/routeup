package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
)

type runSetupOpts struct {
	startAgent bool
	trust      bool
	bind       bool
	useSystem  bool
	tlsPort    int
}

func newSetupCmd() *cobra.Command {
	var (
		noStart   bool
		noTrust   bool
		noBind    bool
		useSystem bool
		tlsPort   int
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Get this machine ready to serve apps over HTTPS",
		Long: "Get this machine ready to serve local apps over HTTPS.\n\n" +
			"Run once. This:\n" +
			"  1. Creates a certificate authority just for your machine and\n" +
			"     adds it to your system trust store, so browsers trust\n" +
			"     https://<name>.localhost with no warnings.\n" +
			"  2. Lets routeup answer on port 443, so your URLs carry no\n" +
			"     port number.\n" +
			"  3. Starts the background agent that routes requests to your apps.\n\n" +
			"You'll confirm once with Touch ID or your password so these\n" +
			"changes can be made. After that, serving a route never asks again.\n\n" +
			"Re-running setup is safe — it skips anything already done.",
		Example: "  routeup setup                # the usual: HTTPS on port 443\n" +
			"  routeup setup --port 8443    # use a high port (no password needed)\n" +
			"  routeup setup --no-trust     # don't touch the system trust store",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tlsPort <= 0 || tlsPort > 65535 {
				return fmt.Errorf("invalid --port value %d (must be 1-65535)", tlsPort)
			}
			return runSetup(cmd, runSetupOpts{
				startAgent: !noStart,
				trust:      !noTrust,
				bind:       !noBind,
				useSystem:  useSystem,
				tlsPort:    tlsPort,
			})
		},
	}

	cmd.Flags().IntVar(&tlsPort, "port", ipc.DefaultUserPort, "HTTPS port for your URLs (use 1024 or higher to skip the password prompt)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "don't start the background agent")
	cmd.Flags().BoolVar(&noTrust, "no-trust", false, "don't add the certificate to your system trust store")
	cmd.Flags().BoolVar(&noBind, "no-bind", false, "don't claim port 443 (serve on a high port instead)")
	cmd.Flags().BoolVar(&useSystem, "system", false, "macOS: force system-wide trust (automatic when binding a privileged port)")
	return cmd
}

func runSetup(cmd *cobra.Command, opts runSetupOpts) error {
	certPath, err := state.CACertPath()
	if err != nil {
		return err
	}
	keyPath, err := state.CAKeyPath()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	caState, _, _ := certs.Inspect(certPath, keyPath)

	needCreate := false
	switch caState {
	case certs.CAPresent:
		_, _ = fmt.Fprintln(out, "certificate authority: already set up")

	case certs.CAPartial, certs.CABroken:
		_, _ = fmt.Fprintln(out, "certificate authority: recreating (the previous one was incomplete)")
		needCreate = true

	case certs.CAAbsent:
		needCreate = true
	}

	if needCreate {
		if _, err := certs.Create(certPath, keyPath); err != nil {
			return fmt.Errorf("creating local CA: %w", err)
		}
		_, _ = fmt.Fprintf(out, "certificate authority: created (%s)\n", certPath)
	}

	if opts.trust {
		useSystem := opts.useSystem || (opts.bind && privbind.Required(opts.tlsPort))
		installCATrust(cmd, out, certPath, useSystem)
	} else {
		_, _ = fmt.Fprintln(out, "certificate: not trusted (--no-trust)")
	}

	if opts.bind {
		installPrivBind(cmd, out, opts.tlsPort)
	} else {
		_, _ = fmt.Fprintln(out, "port setup: skipped (--no-bind)")
	}

	marker := &state.SetupMarker{Version: 1, TLSPort: opts.tlsPort}
	if opts.bind && privbind.Required(opts.tlsPort) {
		if bp, err := privbind.BinaryPath(); err == nil {
			marker.BinPath = bp
		}
	}
	if err := state.WriteSetupMarker(marker); err != nil {
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintf(out, "warning: failed to write setup marker: %v\n", err)
	}

	if !opts.startAgent {
		_, _ = fmt.Fprintln(out, "agent: not started (--no-start)")
		return nil
	}

	return startLocalAgent(cmd, out)
}

// installPrivBind delegates to privbind.Install. No-op for >=1024. Non-fatal.
func installPrivBind(cmd *cobra.Command, out io.Writer, userPort int) {
	if !privbind.Required(userPort) {
		_, _ = fmt.Fprintf(out, "port %d: ready\n", userPort)
		return
	}
	_, _ = fmt.Fprintf(out, "setting up port %d (asks for your password)...\n", userPort)

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if err := privbind.Install(ctx, userPort); err != nil {
		_, _ = fmt.Fprintf(out, "port %d: couldn't set up (%v)\n", userPort, err)
		_, _ = fmt.Fprintln(out, "  rerun `routeup setup`, or use --port with a number 1024 or higher")
		return
	}
	_, _ = fmt.Fprintf(out, "port %d: ready\n", userPort)
}

// installCATrust shells out to the OS trust installer. Non-fatal.
func installCATrust(cmd *cobra.Command, out io.Writer, certPath string, useSystem bool) {
	if useSystem {
		_, _ = fmt.Fprintln(out, "trusting the certificate system-wide (asks for your password)...")
	} else {
		_, _ = fmt.Fprintln(out, "trusting the certificate...")
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	if err := certs.InstallTrust(ctx, certPath, certs.TrustOptions{System: useSystem}); err != nil {
		_, _ = fmt.Fprintf(out, "certificate: not trusted yet (%v)\n", err)
		_, _ = fmt.Fprintln(out, "  rerun `routeup setup` to try again")
		return
	}
	_, _ = fmt.Fprintln(out, "certificate: trusted")
}

// startLocalAgent ensures the agent is up. Non-fatal: serve retries on demand.
func startLocalAgent(cmd *cobra.Command, out io.Writer) error {
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintf(out, "warning: failed to resolve agent socket path: %v\n", err)
		return nil
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
		_, _ = fmt.Fprintf(out, "agent: couldn't start now (%v)\n", err)
		_, _ = fmt.Fprintln(out, "  it'll start on its own the first time you run `routeup serve`")
		return nil
	}
	switch res {
	case agentctl.EnsureAlreadyRunning:
		_, _ = fmt.Fprintln(out, "agent: already running")
	case agentctl.EnsureStarted:
		_, _ = fmt.Fprintln(out, "agent: started")
	case agentctl.EnsureRestarted:
		_, _ = fmt.Fprintln(out, "agent: restarted (build changed)")
	}
	return nil
}
