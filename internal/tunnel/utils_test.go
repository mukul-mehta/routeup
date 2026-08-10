package tunnel

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestYamuxConfig guards the streaming tuning: a future bump of yamux's
// DefaultConfig must not silently revert the raised window or write timeout
// (see OQ-010 and the rationale in utils.go).
func TestYamuxConfig(t *testing.T) {
	c := yamuxConfig()

	if got := c.MaxStreamWindowSize; got != 1<<20 {
		t.Errorf("MaxStreamWindowSize = %d, want %d (1 MiB)", got, 1<<20)
	}
	if got := c.ConnectionWriteTimeout; got != 30*time.Second {
		t.Errorf("ConnectionWriteTimeout = %s, want 30s", got)
	}
	// The raised ceiling must stay above yamux's 256KB initial window, or yamux
	// rejects the config; this also documents that the window can grow.
	if c.MaxStreamWindowSize <= 256*1024 {
		t.Errorf("MaxStreamWindowSize = %d, want > 256KB so the window can grow", c.MaxStreamWindowSize)
	}
}

func TestReadHandshakeMessage(t *testing.T) {
	var encoded bytes.Buffer
	want := HandshakeMessage{Type: msgClaim, Claim: &ClaimSpec{Route: "api"}}
	if err := writeHandshakeMessage(&encoded, want); err != nil {
		t.Fatal(err)
	}
	got, err := readHandshakeMessage(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.Claim == nil || got.Claim.Route != want.Claim.Route {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
}

func TestReadHandshakeMessageRejectsOversizedMessage(t *testing.T) {
	message := `{"type":"claim_err","error":"` + strings.Repeat("x", maxHandshakeMessageSize) + `"}` + "\n"
	if _, err := readHandshakeMessage(strings.NewReader(message)); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("error = %v, want handshake size limit", err)
	}
}

func TestSanitizeRemoteError(t *testing.T) {
	got := sanitizeRemoteError("invalid\x1b[2J\n" + strings.Repeat("x", 300))
	if strings.ContainsAny(got, "\x1b\n") {
		t.Fatalf("sanitized error contains terminal controls: %q", got)
	}
	if !strings.Contains(got, `\u001b[2J\n`) {
		t.Fatalf("sanitized error lost escaped context: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("sanitized error was not bounded: %q", got)
	}
}
