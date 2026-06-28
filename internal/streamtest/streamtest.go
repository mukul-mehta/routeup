// Package streamtest provides synthetic streaming backends used by the M6
// tunnel/proxy tests: a WebSocket "HMR" server (models Vite), an SSE server
// (models Next.js webpack HMR), and a body-echo handler plus a deterministic
// reader for large transfers. They are deterministic and self-contained so the
// streaming acceptance criteria run in CI without a real dev server. (Real Vite
// and Next servers are exercised separately by the build-tagged integration
// tests in internal/server.)
//
// It is a normal (non-_test) package so the tunnel, server, and proxy test
// packages can all import it; it deliberately takes no dependency on testing.
//
// Provenance: generated with the help of Claude (Anthropic) as part of M6
package streamtest

import (
	"io"
	"net/http"

	"github.com/coder/websocket"
)

// WSOptions configures WSHMR.
type WSOptions struct {
	// Subprotocols are offered to the client via Accept. Tests assert the
	// negotiated value round-trips through the proxy chain, which is the
	// cheapest proof the Sec-WebSocket-* headers survived every hop.
	Subprotocols []string
	// Greeting, if non-empty, is sent as a text message immediately after the
	// upgrade — an unprompted server->client push, the HMR-critical direction.
	Greeting string
	// EchoPrefix is prepended to each echoed client message. Defaults to "echo:".
	EchoPrefix string
}

// WSHMR returns a handler that accepts a WebSocket (negotiating opts.Subprotocols),
// optionally sends opts.Greeting as a server push, then echoes every client
// message back prefixed with opts.EchoPrefix until the peer closes. This models
// a Vite HMR endpoint: a long-lived socket with both server-initiated pushes and
// client messages.
func WSHMR(opts WSOptions) http.Handler {
	prefix := opts.EchoPrefix
	if prefix == "" {
		prefix = "echo:"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// InsecureSkipVerify: the Go test client sends no Origin header (so the
		// default same-origin check would pass anyway); this just removes any
		// origin-based flakiness in a test-only backend.
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       opts.Subprotocols,
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()

		ctx := r.Context()
		if opts.Greeting != "" {
			if err := c.Write(ctx, websocket.MessageText, []byte(opts.Greeting)); err != nil {
				return
			}
		}
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return // client closed, or the request context was cancelled
			}
			if err := c.Write(ctx, typ, append([]byte(prefix), data...)); err != nil {
				return
			}
		}
	})
}

// SSEHMR returns a handler that streams events as text/event-stream, flushing
// after each, modelling Next.js webpack HMR (/_next/webpack-hmr).
//
// If gate is non-nil, the handler receives one value from it before each event
// after the first. That lets a test prove incremental, non-buffered delivery
// without timing assumptions: read event 0, then release the gate, then read
// event 1, asserting the first arrived before the second was ever written.
func SSEHMR(events []string, gate <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streamtest: ResponseWriter is not a Flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for i, ev := range events {
			if i > 0 && gate != nil {
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
			if _, err := io.WriteString(w, "data: "+ev+"\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	})
}

// EchoBody returns a handler that streams the request body back unchanged. With
// a large request body it exercises both directions at once (upload + download)
// under flow control.
func EchoBody() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(w, r.Body)
	})
}

// patternReader emits a deterministic, non-repeating-per-byte stream
// (0,1,2,…,255,0,1,…). It is infinite; wrap it in io.LimitReader or use
// io.CopyN. The pattern (rather than zeros) makes truncation or corruption
// visible to a hash comparison.
type patternReader struct{ n byte }

func (p *patternReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = p.n
		p.n++
	}
	return len(b), nil
}

// PatternReader returns a fresh deterministic infinite reader. Two independent
// PatternReaders yield identical bytes, so a sender and a checker can compare
// hashes without buffering the whole stream.
func PatternReader() io.Reader { return &patternReader{} }
