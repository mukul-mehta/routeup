package tunnel

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/hashicorp/yamux"
)

// Tuned for large requests - Need to be verified against actual data
const (
	maxStreamWindow         = 1 << 20
	connectionWriteTimeout  = 30 * time.Second
	maxHandshakeMessageSize = 64 << 10
)

// yamuxConfig returns the shared yamux config: default tuning with the stream
// window and write timeout raised for streaming workloads, and yamux's own
// logging silenced (session errors surface through Run/ServeConn instead).
func yamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	c.MaxStreamWindowSize = maxStreamWindow
	c.ConnectionWriteTimeout = connectionWriteTimeout
	return c
}

// writeHandshakeMessage JSON-encodes one control-stream message onto w.
func writeHandshakeMessage(w io.Writer, msg HandshakeMessage) error {
	return json.NewEncoder(w).Encode(msg)
}

func readHandshakeMessage(r io.Reader) (HandshakeMessage, error) {
	line, err := bufio.NewReaderSize(r, maxHandshakeMessageSize+1).ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > maxHandshakeMessageSize {
		return HandshakeMessage{}, errors.New("handshake message exceeds 64 KiB limit")
	}
	if err != nil {
		return HandshakeMessage{}, err
	}
	var msg HandshakeMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return HandshakeMessage{}, err
	}
	return msg, nil
}

func sanitizeRemoteError(value string) string {
	const maxRunes = 256
	var out strings.Builder
	count := 0
	for _, r := range value {
		if count == maxRunes {
			out.WriteString("...")
			break
		}
		count++
		switch r {
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if unicode.IsControl(r) {
				_, _ = fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	if out.Len() == 0 {
		return "server rejected the claim"
	}
	return out.String()
}

// statusCodeOf pulls an HTTP-style status out of err when it implements
// StatusCode() int (the broker's coded errors), so the server can ferry the
// rejection code to the agent in a claim_err. Returns 0 when err carries none.
func statusCodeOf(err error) int {
	var c interface{ StatusCode() int }
	if errors.As(err, &c) {
		return c.StatusCode()
	}
	return 0
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header
// value, or "" if it is absent or not a bearer scheme.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(header, prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
