package cli

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/mukul-mehta/routeup/internal/logs"
)

func TestWriteInspectCurlPreservesCapturedHeadersAndTargetsLocalRoute(t *testing.T) {
	body := []byte{'p', 'a', 'y', '\'', 0, 0xff}
	entry := logs.Entry{
		ID:          "req_curl",
		Route:       "api.myapp",
		Host:        "api-myapp.alice.routeup.dev",
		Method:      http.MethodPost,
		RequestPath: "/webhooks?event=push",
		Capture: &logs.Capture{Request: &logs.CapturedMessage{
			Headers: http.Header{
				"Authorization":       {"Bearer secret"},
				"Connection":          {"X-Remove-Me"},
				"Cookie":              {"session=secret"},
				"X-Forwarded-For":     {"attacker"},
				"X-Forwarded-Port":    {"22"},
				"X-Real-IP":           {"198.51.100.4"},
				"X-Auth-Key":          {"auth-secret"},
				"X-Remove-Me":         {"connection-specific"},
				"X-Webhook-Signature": {"secret-signature"},
				"X-Webhook-Event":     {"push's $(touch /tmp/bad)"},
			},
			Body:     body,
			Complete: true,
		}},
	}

	var output bytes.Buffer
	if err := writeInspectCurl(&output, entry, 47443); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		"https://api.myapp.localhost:47443/webhooks?event=push",
		"--ipv4",
		"--request 'POST'",
		"--header 'Authorization: Bearer secret'",
		"--header 'Connection: X-Remove-Me'",
		"--header 'Cookie: session=secret'",
		"--header 'X-Forwarded-For: attacker'",
		"--header 'X-Forwarded-Port: 22'",
		"--header 'X-Real-IP: 198.51.100.4'",
		"--header 'X-Auth-Key: auth-secret'",
		"--header 'X-Remove-Me: connection-specific'",
		"--header 'X-Webhook-Signature: secret-signature'",
		"--path-as-is",
		"--header 'X-Webhook-Event: push'\"'\"'s $(touch /tmp/bad)'",
		"--header 'Content-Type:'",
		"--data-binary '@-'",
		base64.StdEncoding.EncodeToString(body),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("curl output %q missing %q", got, want)
		}
	}
	if strings.Contains(got, entry.Host) {
		t.Fatalf("curl output targets captured public host instead of local route: %q", got)
	}
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(got)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated command is not valid shell: %v\n%s\n%s", err, output, got)
	}
}

func TestWriteInspectCurlRequiresCompleteRequestCapture(t *testing.T) {
	tests := []struct {
		name    string
		capture *logs.Capture
		want    string
	}{
		{name: "missing request", capture: &logs.Capture{}, want: "no captured request"},
		{name: "incomplete request", capture: &logs.Capture{Request: &logs.CapturedMessage{}}, want: "capture is incomplete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := writeInspectCurl(&output, logs.Entry{
				ID: "req_test", Route: "myapp", Method: http.MethodGet, RequestPath: "/", Capture: test.capture,
			}, 443)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestWriteInspectCurlEmbedsTextBodyDirectly(t *testing.T) {
	entry := logs.Entry{
		ID: "req_text", Route: "myapp", Method: http.MethodPost, RequestPath: "/hook",
		Capture: &logs.Capture{Request: &logs.CapturedMessage{
			Headers: http.Header{"Content-Type": {"application/json"}},
			Body:    []byte(`{"event":"manual-test"}`), Complete: true,
		}},
	}
	var output bytes.Buffer
	if err := writeInspectCurl(&output, entry, 443); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.HasPrefix(got, "curl ") {
		t.Fatalf("curl output should start directly with curl: %q", got)
	}
	if !strings.Contains(got, `--data-raw '{"event":"manual-test"}'`) {
		t.Fatalf("curl output does not contain direct JSON body: %q", got)
	}
	if strings.Contains(got, "base64") {
		t.Fatalf("text curl output unexpectedly uses base64: %q", got)
	}
}

func TestShellQuoteEscapesApostrophes(t *testing.T) {
	if got, want := shellQuote("it's safe"), `'it'"'"'s safe'`; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
