package tunnel

import "time"

// Timeouts for the tunnel client's connect and reconnect behavior. The serving
// phase has no timeout here: it is bounded by the caller's context (cancelled on
// Unexpose, owner death, or agent shutdown).
const (
	// dialTimeout bounds one WebSocket dial + upgrade in handshake.
	dialTimeout = 15 * time.Second

	// baseBackoff and maxBackoff bound Run's reconnect backoff: it starts at
	// baseBackoff and doubles up to maxBackoff between session attempts.
	baseBackoff = 500 * time.Millisecond
	maxBackoff  = 30 * time.Second

	// requestHeaderTimeout bounds how long the agent's HTTP server waits for a
	// request's headers on a tunnel stream (a slowloris guard).
	requestHeaderTimeout = 10 * time.Second
)
