package tunnel

import (
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
