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
	"golang.org/x/term"

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
	server     string
	token      string
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
			"In a terminal, setup first asks for a public server and token so\n" +
			"`expose` needs no flags later. The server defaults to\n" +
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
			return runSetup(cmd, runSetupOpts{
				startAgent: !noStart,
				trust:      !noTrust,
				bind:       !noBind,
				useSystem:  useSystem,
				tlsPort:    tlsPort,
				server:     server,
				token:      token,
			})
		},
	}

	cmd.Flags().IntVar(&tlsPort, "port", ipc.DefaultUserPort, "HTTPS port for your URLs (use 1024 or higher to skip the password prompt)")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "don't start the background agent")
	cmd.Flags().BoolVar(&noTrust, "no-trust", false, "don't add the certificate to your system trust store")
	cmd.Flags().BoolVar(&noBind, "no-bind", false, "don't claim port 443 (serve on a high port instead)")
	cmd.Flags().BoolVar(&useSystem, "system", false, "macOS: force system-wide trust (automatic when binding a privileged port)")
	cmd.Flags().StringVar(&server, "server", "", "public server URL to save for expose (e.g. https://edge.routeup.dev)")
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

	// Ask up front (interactive terminals only) where to publish routes, so the
	// rest of setup runs unattended. Flags and non-interactive runs skip this.
	promptServerCreds(cmd, out, &opts)

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

	saveClientCreds(out, opts.server, opts.token)

	if !opts.startAgent {
		_, _ = fmt.Fprintln(out, "agent: not started (--no-start)")
		return nil
	}

	return startLocalAgent(cmd, out)
}

// defaultPublicServer is the hosted routeup server offered as the default answer
// to the setup server prompt, so a fresh machine just presses Enter to publish
// through it. It's the edge control host that expose talks to, not the apex.
const defaultPublicServer = "https://edge.routeup.dev"

// serverCredPrompter reads the interactive setup answers. readSecret reads the
// token without echoing it; it's a field so tests can supply a fake.
type serverCredPrompter struct {
	in         *bufio.Reader
	out        io.Writer
	readSecret func() (string, error)
}

// promptServerCreds asks, at the top of setup, where to publish routes. It runs
// only on a real terminal and only for fields the user didn't already pass as
// flags, so scripts, CI, and tests keep using flags and never block on input.
// The token is read masked. See collect for the question flow.
func promptServerCreds(cmd *cobra.Command, out io.Writer, opts *runSetupOpts) {
	serverFromFlag := cmd.Flags().Changed("server")
	tokenFromFlag := cmd.Flags().Changed("token")
	if serverFromFlag && tokenFromFlag {
		return // nothing left to ask
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return // non-interactive: flags are the only input
	}

	cc, _ := state.ReadClientConfig()
	p := serverCredPrompter{
		in:  bufio.NewReader(cmd.InOrStdin()),
		out: out,
		readSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			_, _ = fmt.Fprintln(out) // ReadPassword swallows the Enter; print it back
			return string(b), err
		},
	}
	p.collect(cc, serverFromFlag, tokenFromFlag, opts)
}

// collect runs the question flow: ask for the server (offering any saved value
// as the default); only if a server results does it ask for the token, read
// masked. Blank answers keep saved values. Flag-provided fields are left as-is.
func (p serverCredPrompter) collect(cc state.ClientConfig, serverFromFlag, tokenFromFlag bool, opts *runSetupOpts) {
	if !serverFromFlag {
		def := cc.Server
		if def == "" {
			def = defaultPublicServer
		}
		answer := p.line("Public server URL for `expose` (leave empty for default, 'none' to stay local)", def)
		if strings.EqualFold(answer, "none") {
			answer = "" // explicit opt-out of a public server
		}
		opts.server = answer
	}
	if opts.server == "" {
		return // no server, so a token has nothing to authenticate against
	}
	if tokenFromFlag {
		return
	}
	tok, err := p.secret(fmt.Sprintf("Token for %s (blank to keep current)", opts.server))
	if err != nil {
		_, _ = fmt.Fprintf(p.out, "  (skipping token: %v)\n", err)
		return
	}
	switch {
	case tok != "":
		opts.token = tok
	case cc.Token != "":
		opts.token = cc.Token // kept the saved one
	}
}

// line prompts with an optional [default] and returns the trimmed answer, or
// def when the user just presses Enter.
func (p serverCredPrompter) line(label, def string) string {
	if def != "" {
		_, _ = fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", label)
	}
	line, _ := p.in.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

// secret prompts for and returns a value read without echo.
func (p serverCredPrompter) secret(label string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "%s: ", label)
	s, err := p.readSecret()
	return strings.TrimSpace(s), err
}

// saveClientCreds persists the server URL and/or token to the client config so
// `expose` and `serve --expose` need no flags. It merges, leaving unset fields
// untouched, and never prints the token.
func saveClientCreds(out io.Writer, server, token string) {
	if server == "" && token == "" {
		return
	}
	cc, _ := state.ReadClientConfig()
	if server != "" {
		cc.Server = server
	}
	if token != "" {
		cc.Token = token
	}
	if err := state.WriteClientConfig(cc); err != nil {
		_, _ = fmt.Fprintf(out, "warning: couldn't save server/token: %v\n", err)
		return
	}
	if server != "" {
		_, _ = fmt.Fprintf(out, "server: saved (%s)\n", server)
	}
	if token != "" {
		_, _ = fmt.Fprintln(out, "token: saved")
	}
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
