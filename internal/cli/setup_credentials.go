package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mukul-mehta/routeup/internal/state"
)

const defaultPublicServer = "https://edge.routeup.dev"

type serverCredPrompter struct {
	in         *bufio.Reader
	out        io.Writer
	readSecret func() (string, error)
}

func promptServerCreds(cmd *cobra.Command, out io.Writer, opts *runSetupOpts) error {
	serverFromFlag := cmd.Flags().Changed("server")
	tokenFromFlag := cmd.Flags().Changed("token")
	if serverFromFlag && tokenFromFlag {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil
	}

	cc, err := state.ReadClientConfig()
	if err != nil {
		return err
	}
	p := serverCredPrompter{
		in:  bufio.NewReader(cmd.InOrStdin()),
		out: out,
		readSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			_, _ = fmt.Fprintln(out)
			return string(b), err
		},
	}
	return p.collect(cc, serverFromFlag, tokenFromFlag, opts)
}

// collect asks only for credentials not supplied by flags. Blank answers keep
// saved values, while choosing "none" explicitly clears both fields.
func (p serverCredPrompter) collect(cc state.ClientConfig, serverFromFlag, tokenFromFlag bool, opts *runSetupOpts) error {
	if !serverFromFlag {
		styles := newTerminalStyles(p.out)
		_, _ = fmt.Fprintf(p.out, "\n  %s\n", styles.muted("Public server for routeup expose (optional — press Enter to accept, 'none' to skip):"))
		def := cc.Server
		if def == "" {
			def = defaultPublicServer
		}
		answer := p.line("  server", def)
		if strings.EqualFold(answer, "none") {
			if tokenFromFlag && strings.TrimSpace(opts.token) != "" {
				return errors.New("--token cannot be combined with choosing 'none' for the public server")
			}
			answer = ""
			opts.clearClient = true
		}
		opts.server = answer
	}
	if opts.server == "" || tokenFromFlag {
		return nil
	}

	styles := newTerminalStyles(p.out)
	_, _ = fmt.Fprintf(p.out, "  %s%s%s\n",
		styles.muted("Tokens authorize persistent public routes. Get one from "),
		styles.url("https://routeup.dev"),
		styles.muted(", or press Enter to skip:"),
	)
	tok, err := p.secret("  token")
	if err != nil {
		_, _ = fmt.Fprintln(p.out, newTerminalStyles(p.out).warning(fmt.Sprintf("  (skipping token: %v)", err)))
		return nil
	}
	if tok != "" {
		opts.token = tok
	} else if cc.Token != "" && sameServer(cc.Server, opts.server) {
		opts.token = cc.Token
	}
	return nil
}

func (p serverCredPrompter) line(label, def string) string {
	styles := newTerminalStyles(p.out)
	if def != "" {
		_, _ = fmt.Fprintf(p.out, "%s %s: ", styles.label(label), styles.muted("["+def+"]"))
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", styles.label(label))
	}
	line, _ := p.in.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

func (p serverCredPrompter) secret(label string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "%s: ", newTerminalStyles(p.out).label(label))
	s, err := p.readSecret()
	return strings.TrimSpace(s), err
}

// saveClientCreds merges credentials without ever carrying a token to a
// different server or allowing a token to exist without a server identity.
func saveClientCreds(out io.Writer, server, token string, clear bool) error {
	styles := newTerminalStyles(out)
	if clear {
		if err := state.WriteClientConfig(state.ClientConfig{}); err != nil {
			return fmt.Errorf("clear server/token: %w", err)
		}
		_, _ = fmt.Fprintln(out, styles.stepOK("server/token", "cleared"))
		return nil
	}
	if server == "" && token == "" {
		return nil
	}

	cc, err := state.ReadClientConfig()
	if err != nil {
		return err
	}
	if server != "" {
		if token == "" && !sameServer(server, cc.Server) {
			cc.Token = ""
		}
		cc.Server = server
	}
	if token != "" {
		if server == "" && cc.Server == "" {
			return errors.New("a token requires a public server")
		}
		cc.Token = token
	}
	if err := state.WriteClientConfig(cc); err != nil {
		return fmt.Errorf("save server/token: %w", err)
	}
	if server != "" {
		_, _ = fmt.Fprintln(out, styles.stepOK("server", "saved", terminalEscapeString(server)))
	}
	if token != "" {
		_, _ = fmt.Fprintln(out, styles.stepOK("token", "saved"))
	}
	return nil
}
