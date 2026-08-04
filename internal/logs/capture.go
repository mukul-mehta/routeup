// Capture is the optional original-request data retained with one log Entry.
//
// Capture exists because a later proxy needs to inspect a body without changing
// the bytes or errors that reach the upstream service. Reading an HTTP body into
// memory before proxying would break streaming and allow an arbitrarily large
// request to exhaust the agent.
//
// MessageCapture hides that bookkeeping from the proxy. It copies headers,
// exposes an io.ReadCloser the proxy can use normally, then creates a
// CapturedMessage when Take is called after forwarding ends.
//
//	incoming HTTP message
//	  -> NewMessageCapture
//	  -> proxy reads MessageCapture as its body
//	  -> Take returns CapturedMessage
//	  -> request field on Capture
//
// A captured request is limited to 256 KiB, including headers and body.
// Complete is false when the full message did not fit or the body did not reach
// EOF. Inspect may still display the retained prefix.
package logs

import (
	"io"
	"net/http"
	"sort"
	"sync"
)

const maxCapturedMessageBytes = 256 << 10

// CapturedMessage is one captured HTTP request.
type CapturedMessage struct {
	Headers  http.Header `json:"headers,omitempty"`
	Body     []byte      `json:"body,omitempty"`
	Complete bool        `json:"complete"`
}

// Capture holds the original request data retained for an inspected entry.
type Capture struct {
	Request CapturedMessage `json:"request"`
}

// MessageCapture is an io.ReadCloser that forwards its source body unchanged
// while retaining the bounded data needed to build one CapturedMessage.
type MessageCapture struct {
	mu sync.Mutex

	headers         http.Header
	headersComplete bool
	body            io.ReadCloser
	limit           int
	data            []byte
	bodyComplete    bool
	truncated       bool
}

// NewMessageCapture starts a bounded capture for one request's headers and
// body. The returned value can replace an HTTP request body directly.
func NewMessageCapture(headers http.Header, body io.ReadCloser) *MessageCapture {
	capturedHeaders, remaining, complete := captureHeaders(headers, maxCapturedMessageBytes)
	bodyComplete := body == nil || body == http.NoBody
	if body == nil {
		body = http.NoBody
	}
	return &MessageCapture{
		headers:         capturedHeaders,
		headersComplete: complete,
		body:            body,
		limit:           remaining,
		data:            make([]byte, 0, remaining),
		bodyComplete:    bodyComplete,
	}
}

// Read forwards to the source body and retains at most the remaining message
// capacity.
func (capture *MessageCapture) Read(p []byte) (int, error) {
	n, err := capture.body.Read(p)
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if n > 0 {
		capture.appendLocked(p[:n])
	}
	if err == io.EOF {
		capture.bodyComplete = true
	}
	return n, err
}

// Close closes the source body.
func (capture *MessageCapture) Close() error {
	return capture.body.Close()
}

// Take returns a copy of the retained data as it exists at call time. A reverse
// proxy can finish an exchange after an upstream returns early, so this remains
// race-safe even if a transport is still closing the request body.
func (capture *MessageCapture) Take() CapturedMessage {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return CapturedMessage{
		Headers:  capture.headers,
		Body:     append([]byte(nil), capture.data...),
		Complete: capture.headersComplete && capture.bodyComplete && !capture.truncated,
	}
}

func (capture *MessageCapture) appendLocked(data []byte) {
	remaining := capture.limit - len(capture.data)
	if remaining == 0 {
		capture.truncated = true
		return
	}
	if len(data) > remaining {
		capture.data = append(capture.data, data[:remaining]...)
		capture.truncated = true
		return
	}
	capture.data = append(capture.data, data...)
}

func captureHeaders(headers http.Header, limit int) (http.Header, int, bool) {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	captured := make(http.Header)
	remaining := limit
	for _, key := range keys {
		for _, value := range headers[key] {
			cost := len(key) + len(value)
			if cost > remaining {
				return captured, remaining, false
			}
			captured.Add(key, value)
			remaining -= cost
		}
	}
	return captured, remaining, true
}

func (capture *Capture) clone() *Capture {
	if capture == nil {
		return nil
	}
	out := *capture
	out.Request.Headers = capture.Request.Headers.Clone()
	out.Request.Body = append([]byte(nil), capture.Request.Body...)
	return &out
}

func (capture *Capture) withinLimit() bool {
	if capture == nil {
		return true
	}
	return messageBytes(capture.Request) <= maxCapturedMessageBytes
}

func messageBytes(message CapturedMessage) int {
	return headerBytes(message.Headers) + len(message.Body)
}

func headerBytes(headers http.Header) int {
	bytes := 0
	for key, values := range headers {
		for _, value := range values {
			bytes += len(key) + len(value)
		}
	}
	return bytes
}
