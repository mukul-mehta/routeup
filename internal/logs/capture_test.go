package logs

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMessageCaptureCopiesHeadersWithinMessageLimit(t *testing.T) {
	headers := http.Header{"X-Test": {"original"}}
	capture := NewMessageCapture(headers, io.NopCloser(strings.NewReader("hello")), nil)

	forwarded, err := io.ReadAll(capture)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if got := string(forwarded); got != "hello" {
		t.Fatalf("forwarded body = %q, want hello", got)
	}

	message := capture.Take()
	if !message.Complete || message.Headers.Get("X-Test") != "original" || string(message.Body) != "hello" {
		t.Fatalf("captured message = %#v, want complete original/hello", message)
	}
	message.Headers.Set("X-Test", "changed")
	if got := headers.Get("X-Test"); got != "original" {
		t.Fatalf("source header changed to %q", got)
	}
}

func TestMessageCaptureMarksOversizedHeadersIncomplete(t *testing.T) {
	headers := http.Header{"X-Large": {strings.Repeat("x", maxCapturedMessageBytes)}}
	capture := NewMessageCapture(headers, io.NopCloser(strings.NewReader("body")), nil)

	forwarded, err := io.ReadAll(capture)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if got := string(forwarded); got != "body" {
		t.Fatalf("forwarded body = %q, want body", got)
	}

	message := capture.Take()
	if message.Complete {
		t.Fatal("captured message unexpectedly complete")
	}
	if len(message.Headers) != 0 || string(message.Body) != "body" {
		t.Fatalf("captured message = %#v, want omitted headers and retained body", message)
	}
}

func TestMessageCaptureTruncatesWithoutChangingStream(t *testing.T) {
	body := strings.Repeat("x", maxCapturedMessageBytes+1)
	capture := NewMessageCapture(http.Header{}, io.NopCloser(strings.NewReader(body)), nil)

	forwarded, err := io.ReadAll(capture)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if got := string(forwarded); got != body {
		t.Fatalf("forwarded body length = %d, want %d", len(got), len(body))
	}

	message := capture.Take()
	if message.Complete {
		t.Fatal("captured message unexpectedly complete")
	}
	if len(message.Body) != maxCapturedMessageBytes {
		t.Fatalf("captured body length = %d, want %d", len(message.Body), maxCapturedMessageBytes)
	}
}

func TestMessageCaptureMarksNoBodyComplete(t *testing.T) {
	capture := NewMessageCapture(http.Header{"X-Test": {"original"}}, http.NoBody, nil)
	message := capture.Take()
	if !message.Complete || message.Headers.Get("X-Test") != "original" || len(message.Body) != 0 {
		t.Fatalf("captured message = %#v, want complete empty body", message)
	}
}

func TestMessageCaptureExcludesRedactedHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer secret"},
		"Cookie":        {"session=secret"},
		"X-Trace":       {"keep-me"},
	}
	capture := NewMessageCapture(headers, io.NopCloser(strings.NewReader("payload")), []string{"authorization", "COOKIE"})

	forwarded, err := io.ReadAll(capture)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if string(forwarded) != "payload" {
		t.Fatalf("forwarded body = %q, want payload", forwarded)
	}

	message := capture.Take()
	if message.Headers.Get("Authorization") != "" || message.Headers.Get("Cookie") != "" {
		t.Fatalf("redacted headers retained: %#v", message.Headers)
	}
	if message.Headers.Get("X-Trace") != "keep-me" {
		t.Fatalf("non-redacted header missing: %#v", message.Headers)
	}
	if got, want := strings.Join(message.RedactedHeaders, ","), "authorization,cookie"; got != want {
		t.Fatalf("redacted headers = %q, want %q", got, want)
	}
}
