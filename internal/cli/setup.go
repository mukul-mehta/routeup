package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
)

type runSetupOpts struct {
	startAgent  bool
	trust       bool
	bind        bool
	useSystem   bool
	tlsPort     int
	server      string
	token       string
	clearClient bool
}

func newSetupCmd() *cobra.Command {
	var (
		noStart   bool
		noTrust   bool
		noBind    bool
		useSystem bool
		tlsPort   int
		server    string
		token     string
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
			"After local setup, you'll be asked for a public server and token\n" +
			"so `expose` needs no flags later. The server defaults to\n" +
			"https://edge.routeup.dev — press Enter to accept, or type 'none' to\n" +
			"stay local. Pass --server/--token to skip those questions.\n\n" +
			"Re-running setup is safe — it skips anything already done.",
		Example: "  routeup setup                # the usual: HTTPS on port 443\n" +
			"  routeup setup --port 8443    # use a high port (no password needed)\n" +
			"  routeup setup --no-trust     # don't touch the system trust store",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tlsPort <= 0 || tlsPort > 65535 {
				return fmt.Errorf("invalid --port value %d (must be 1-65535)", tlsPort)
			}
			if noBind && privbind.Required(tlsPort) {
				return fmt.Errorf("--no-bind requires --port 1024 or higher (got %d)", tlsPort)
			}
			clearClient := strings.EqualFold(strings.TrimSpace(server), "none")
			if clearClient {
				if strings.TrimSpace(token) != "" {
					return errors.New("--server none cannot be combined with --token")
				}
				server = ""
				token = ""
			}
			return runSetup(cmd, runSetupOpts{
				startAgent:  !noStart,
				trust:       !noTrust,
				bind:        !noBind,
				useSystem:   useSystem,
				tlsPort:     tlsPort,
				server:      server,
				token:       token,
				clearClient: clearClient,
			})
		},
	}

	cmd.Flags().IntVar(&tlsPort, "port", ipc.DefaultUserPort, "HTTPS port for your URLs (use 1024 or higher to skip the password prompt)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "don't start the background agent")
	cmd.Flags().BoolVar(&noTrust, "no-trust", false, "don't add the certificate to your system trust store")
	cmd.Flags().BoolVar(&noBind, "no-bind", false, "skip port setup (requires --port 1024 or higher)")
	cmd.Flags().BoolVar(&useSystem, "system", false, "macOS: force system-wide trust (automatic when binding a privileged port)")
	cmd.Flags().StringVar(&server, "server", "", "public server URL to save for expose, or 'none' to clear saved credentials")
	cmd.Flags().StringVar(&token, "token", "", "server token to save for expose")
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
	styles := newTerminalStyles(out)

	caState, _, _ := certs.Inspect(certPath, keyPath)

	needCreate := false
	switch caState {
	case certs.CAPresent:
		_, _ = fmt.Fprintln(out, styles.stepOK("certificate authority", "already set up"))

	case certs.CAPartial, certs.CABroken:
		_, _ = fmt.Fprintln(out, styles.stepRun("certificate authority", "recreating (previous was incomplete)"))
		needCreate = true

	case certs.CAAbsent:
		needCreate = true
	}

	if needCreate {
		if err := state.RemoveSetupMarker(); err != nil {
			return fmt.Errorf("invalidate previous setup: %w", err)
		}
		if _, err := certs.Create(certPath, keyPath); err != nil {
			return fmt.Errorf("creating local CA: %w", err)
		}
		_, _ = fmt.Fprintln(out, styles.stepOK("certificate authority", "created", terminalEscapeString(certPath)))
	}

	if opts.trust {
		useSystem := opts.useSystem || (opts.bind && privbind.Required(opts.tlsPort))
		if err := ensureCATrust(cmd, out, certPath, useSystem); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintln(out, styles.stepSkip("certificate", "trust unchanged (--no-trust)"))
	}

	if opts.bind {
		if err := installPrivBind(cmd, out, opts.tlsPort); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintln(out, styles.stepSkip("port", "skipped (--no-bind)"))
	}

	marker := &state.SetupMarker{Version: state.CurrentSetupVersion, TLSPort: opts.tlsPort}
	if opts.bind && privbind.Required(opts.tlsPort) {
		if bp, err := privbind.BinaryPath(); err == nil {
			marker.BinPath = bp
		}
	}
	if err := state.WriteSetupMarker(marker); err != nil {
		return fmt.Errorf("write setup marker: %w", err)
	}

	if err := promptServerCreds(cmd, out, &opts); err != nil {
		return err
	}
	if opts.server != "" {
		opts.server, err = normalizeServerURL(opts.server)
		if err != nil {
			return err
		}
	}

	if err := saveClientCreds(out, opts.server, opts.token, opts.clearClient); err != nil {
		return err
	}

	if !opts.startAgent {
		_, _ = fmt.Fprintln(out, styles.stepSkip("agent", "not started (--no-start)"))
		printSetupSummary(out, styles)
		return nil
	}

	if err := startLocalAgent(cmd, out); err != nil {
		return err
	}
	printSetupSummary(out, styles)
	return nil
}

func printSetupSummary(out io.Writer, styles terminalStyles) {
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "  "+styles.accent("routeup is ready"))
	_, _ = fmt.Fprintf(out, "  %s\n", styles.muted("try: routeup serve example-app --port 3000"))
	_, _ = fmt.Fprintf(out, "  %s\n", styles.muted("run routeup --help to see all commands"))
}
