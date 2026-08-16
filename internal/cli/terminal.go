package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type terminalStyles struct {
	enabled bool
	tty     bool
}

func newTerminalStyles(out io.Writer) terminalStyles {
	_, noColor := os.LookupEnv("NO_COLOR")
	tty := terminalIsTTY(out)
	return terminalStyles{enabled: tty && !noColor && os.Getenv("TERM") != "dumb", tty: tty}
}

func terminalIsInteractive(in io.Reader, out io.Writer) bool {
	return terminalIsTTY(in) && terminalIsTTY(out) && os.Getenv("TERM") != "dumb"
}

func terminalIsTTY(value any) bool {
	file, ok := value.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

func (s terminalStyles) render(style lipgloss.Style, value string) string {
	if !s.enabled {
		return value
	}
	return style.Render(value)
}

func (s terminalStyles) accent(value string) string {
	return s.render(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{
		Light: "#5F3DC4",
		Dark:  "#B197FC",
	}), value)
}

func (s terminalStyles) label(value string) string {
	return s.render(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{
		Light: "#495057",
		Dark:  "#ADB5BD",
	}), value)
}

func (s terminalStyles) muted(value string) string {
	return s.render(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#6C757D",
		Dark:  "#868E96",
	}), value)
}

func (s terminalStyles) success(value string) string {
	return s.render(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#2B8A3E",
		Dark:  "#69DB7C",
	}), value)
}

func (s terminalStyles) warning(value string) string {
	return s.render(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#E67700",
		Dark:  "#FFD43B",
	}), value)
}

func (s terminalStyles) failure(value string) string {
	return s.render(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
		Light: "#C92A2A",
		Dark:  "#FF8787",
	}), value)
}

func (s terminalStyles) url(value string) string {
	return s.render(lipgloss.NewStyle().Underline(true).Foreground(lipgloss.AdaptiveColor{
		Light: "#1971C2",
		Dark:  "#74C0FC",
	}), value)
}

func (s terminalStyles) statusCode(status int) string {
	value := fmt.Sprintf("%d", status)
	switch {
	case status >= 500:
		return s.failure(value)
	case status >= 400:
		return s.warning(value)
	case status >= 200 && status < 400:
		return s.success(value)
	default:
		return s.muted(value)
	}
}

// PrintError writes a CLI error with terminal-safe styling when out is a TTY.
func PrintError(out io.Writer, err error) {
	printError(out, err, newTerminalStyles(out))
}

func printError(out io.Writer, err error, styles terminalStyles) {
	if !styles.tty {
		_, _ = fmt.Fprintln(out, err)
		return
	}
	lines := strings.Split(err.Error(), "\n")
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.failure("error:"), terminalEscapeString(lines[0]))
	for _, line := range lines[1:] {
		_, _ = fmt.Fprintf(out, "       %s\n", terminalEscapeString(strings.TrimSpace(line)))
	}
}

// terminalEscapeBytes returns an unambiguous printable-ASCII representation of
// untrusted bytes so captured traffic cannot emit terminal control sequences.
func terminalEscapeBytes(value []byte) string {
	var out strings.Builder
	for _, b := range value {
		switch {
		case b == '\\':
			out.WriteString(`\\`)
		case b == '\n':
			out.WriteString(`\n`)
		case b == '\r':
			out.WriteString(`\r`)
		case b == '\t':
			out.WriteString(`\t`)
		case b >= 0x20 && b <= 0x7e:
			out.WriteByte(b)
		default:
			_, _ = fmt.Fprintf(&out, `\x%02x`, b)
		}
	}
	return out.String()
}

func terminalEscapeString(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			_, _ = fmt.Fprintf(&out, `\x%02x`, r)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
