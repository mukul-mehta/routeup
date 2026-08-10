package cli

import (
	"fmt"
	"strings"
)

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
	return terminalEscapeBytes([]byte(value))
}
