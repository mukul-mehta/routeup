package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mukul-mehta/routeup/internal/state"
)

const (
	defaultPublicServer = "https://edge.routeup.dev"
	savedTokenMask      = "********"
	maxTokenLength      = 4096
)

var errTokenPromptInterrupted = errors.New("token prompt interrupted")

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
			return readMaskedSecret(os.Stdin, out)
		},
	}
	return p.collect(cc, serverFromFlag, tokenFromFlag, opts)
}

// collect asks only for credentials not supplied by flags. Blank answers keep
// saved values. Choosing "none" for the server clears both fields; choosing it
// for the token clears only the token.
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
	hasSavedToken := cc.Token != "" && sameServer(cc.Server, opts.server)
	promptSuffix := ", or press Enter to skip:"
	if hasSavedToken {
		promptSuffix = ". Press Enter to keep the saved token, or type 'none' to clear it:"
	}
	_, _ = fmt.Fprintf(p.out, "  %s%s%s\n",
		styles.muted("Tokens authorize persistent public routes. Get one from "),
		styles.url("https://routeup.dev"),
		styles.muted(promptSuffix),
	)
	tok, err := p.secret("  token", hasSavedToken)
	if err != nil {
		if errors.Is(err, errTokenPromptInterrupted) {
			return err
		}
		_, _ = fmt.Fprintln(p.out, newTerminalStyles(p.out).warning(fmt.Sprintf("  (skipping token: %v)", err)))
		return nil
	}
	if strings.EqualFold(tok, "none") {
		opts.token = ""
		opts.clearToken = true
	} else if tok != "" {
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

func (p serverCredPrompter) secret(label string, saved bool) (string, error) {
	styles := newTerminalStyles(p.out)
	if saved {
		_, _ = fmt.Fprintf(p.out, "%s %s: ", styles.label(label), styles.muted("["+savedTokenMask+"]"))
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", styles.label(label))
	}
	s, err := p.readSecret()
	return strings.TrimSpace(s), err
}

func readMaskedSecret(in *os.File, out io.Writer) (string, error) {
	fd := int(in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("prepare token prompt: %w", err)
	}

	secret, readErr := readMaskedInput(in, out)
	restoreErr := term.Restore(fd, oldState)
	_, newlineErr := fmt.Fprintln(out)
	if readErr != nil {
		return "", readErr
	}
	if restoreErr != nil {
		return "", fmt.Errorf("restore terminal after token prompt: %w", restoreErr)
	}
	if newlineErr != nil {
		return "", fmt.Errorf("finish token prompt: %w", newlineErr)
	}
	return secret, nil
}

func readMaskedInput(in io.Reader, out io.Writer) (string, error) {
	reader := bufio.NewReader(in)
	secret := make([]rune, 0, 64)
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			return "", err
		}

		switch r {
		case '\r', '\n':
			return string(secret), nil
		case '\x03':
			return "", errTokenPromptInterrupted
		case '\x04':
			if len(secret) == 0 {
				return "", io.EOF
			}
			return string(secret), nil
		case '\b', '\x7f':
			if len(secret) == 0 {
				continue
			}
			secret = secret[:len(secret)-1]
			if _, err := io.WriteString(out, "\b \b"); err != nil {
				return "", fmt.Errorf("update token mask: %w", err)
			}
		case '\x15':
			for range secret {
				if _, err := io.WriteString(out, "\b \b"); err != nil {
					return "", fmt.Errorf("clear token mask: %w", err)
				}
			}
			secret = secret[:0]
		default:
			if unicode.IsControl(r) {
				continue
			}
			if len(secret) >= maxTokenLength {
				return "", errors.New("token is too long")
			}
			secret = append(secret, r)
			if _, err := io.WriteString(out, "*"); err != nil {
				return "", fmt.Errorf("write token mask: %w", err)
			}
		}
	}
}

// saveClientCreds merges credentials without ever carrying a token to a
// different server or allowing a token to exist without a server identity.
func saveClientCreds(out io.Writer, server, token string, clearClient, clearToken bool) error {
	styles := newTerminalStyles(out)
	if clearClient {
		if err := state.WriteClientConfig(state.ClientConfig{}); err != nil {
			return fmt.Errorf("clear server/token: %w", err)
		}
		_, _ = fmt.Fprintln(out, styles.stepOK("server/token", "cleared"))
		return nil
	}
	if server == "" && token == "" && !clearToken {
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
	if clearToken {
		cc.Token = ""
	}
	if err := state.WriteClientConfig(cc); err != nil {
		return fmt.Errorf("save server/token: %w", err)
	}
	if server != "" {
		_, _ = fmt.Fprintln(out, styles.stepOK("server", "saved", terminalEscapeString(server)))
	}
	if token != "" {
		_, _ = fmt.Fprintln(out, styles.stepOK("token", "saved"))
	} else if clearToken {
		_, _ = fmt.Fprintln(out, styles.stepOK("token", "cleared"))
	}
	return nil
}
