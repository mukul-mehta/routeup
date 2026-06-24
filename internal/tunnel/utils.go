package tunnel

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/hashicorp/yamux"
)

// yamuxConfig returns the shared yamux config: default tuning with yamux's own
// logging silenced (session errors surface through Run/ServeConn instead).
func yamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	return c
}

// writeHandshakeMessage JSON-encodes one control-stream message onto w.
func writeHandshakeMessage(w io.Writer, msg HandshakeMessage) error {
	return json.NewEncoder(w).Encode(msg)
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
