// Capture is the optional request/response data retained with one log Entry.
//
// Capture exists because the proxy needs to inspect a body without changing
// the bytes or errors that reach the upstream service or the client. Reading
// an HTTP body into memory before proxying would break streaming and allow an
// arbitrarily large message to exhaust the agent.
//
// MessageCapture hides that bookkeeping. It copies headers, exposes an
// io.ReadCloser the proxy can use normally, then creates a CapturedMessage
// when Take is called after forwarding ends.
//
//	incoming HTTP message
//	  -> NewMessageCapture
//	  -> proxy reads MessageCapture as its body
//	  -> Take returns CapturedMessage
//	  -> Request or Response field on Capture
//
// Each captured message is limited to 256 KiB, including headers and body.
// Complete is false when the full message did not fit or the body did not reach
// EOF. Inspect may still display the retained prefix.
package logs

import (
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const maxCapturedMessageBytes = 256 << 10

// CapturedMessage is one captured HTTP message (request or response).
type CapturedMessage struct {
	Headers         http.Header `json:"headers,omitempty"`
	RedactedHeaders []string    `json:"redacted_headers,omitempty"`
	Body            []byte      `json:"body,omitempty"`
	Complete        bool        `json:"complete"`
}

// Capture holds the optional request and response data retained for inspect.
type Capture struct {
	Request  *CapturedMessage `json:"request,omitempty"`
	Response *CapturedMessage `json:"response,omitempty"`
}

// MessageCapture is an io.ReadCloser that forwards its source body unchanged
// while retaining the bounded data needed to build one CapturedMessage.
type MessageCapture struct {
	mu sync.Mutex

	headers         http.Header
	redactedHeaders []string
	headersComplete bool
	body            io.ReadCloser
	limit           int
	data            []byte
	bodyComplete    bool
	truncated       bool
}

// NewMessageCapture starts a bounded capture for one message's headers and
// body. The returned value can replace an HTTP request or response body directly.
func NewMessageCapture(headers http.Header, body io.ReadCloser, redactHeaders []string) *MessageCapture {
	redacted := redactedHeaderSet(redactHeaders)
	capturedHeaders, remaining, complete := captureHeaders(headers, maxCapturedMessageBytes, redacted)
	bodyComplete := body == nil || body == http.NoBody
	if body == nil {
		body = http.NoBody
	}
	return &MessageCapture{
		headers:         capturedHeaders,
		redactedHeaders: sortedHeaderNames(redacted),
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
// race-safe even if a transport is still closing the body.
func (capture *MessageCapture) Take() CapturedMessage {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return CapturedMessage{
		Headers:         capture.headers,
		RedactedHeaders: append([]string(nil), capture.redactedHeaders...),
		Body:            append([]byte(nil), capture.data...),
		Complete:        capture.headersComplete && capture.bodyComplete && !capture.truncated,
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

func captureHeaders(headers http.Header, limit int, redacted map[string]struct{}) (http.Header, int, bool) {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	captured := make(http.Header)
	remaining := limit
	for _, key := range keys {
		if _, excluded := redacted[strings.ToLower(key)]; excluded {
			continue
		}
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

func redactedHeaderSet(headers []string) map[string]struct{} {
	set := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header)
		if name != "" {
			set[strings.ToLower(name)] = struct{}{}
		}
	}
	return set
}

func sortedHeaderNames(headers map[string]struct{}) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (capture *Capture) clone() *Capture {
	if capture == nil {
		return nil
	}
	out := *capture
	out.Request = cloneCapturedMessage(capture.Request)
	out.Response = cloneCapturedMessage(capture.Response)
	return &out
}

func cloneCapturedMessage(message *CapturedMessage) *CapturedMessage {
	if message == nil {
		return nil
	}
	out := *message
	out.Headers = message.Headers.Clone()
	out.RedactedHeaders = append([]string(nil), message.RedactedHeaders...)
	out.Body = append([]byte(nil), message.Body...)
	return &out
}

func (capture *Capture) withinLimit() bool {
	if capture == nil {
		return true
	}
	return messageBytes(capture.Request) <= maxCapturedMessageBytes &&
		messageBytes(capture.Response) <= maxCapturedMessageBytes
}

func messageBytes(message *CapturedMessage) int {
	if message == nil {
		return 0
	}
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
