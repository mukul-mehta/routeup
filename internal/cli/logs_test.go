package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/route"
)

func TestWriteLogEntryTextAndJSON(t *testing.T) {
	entry := logs.Entry{
		ID:          "req_Ap7kQ3mN8vR2xLzC",
		StartedAt:   time.Date(2026, time.August, 4, 12, 41, 3, 0, time.Local),
		Duration:    38 * time.Millisecond,
		Source:      logs.SourcePublic,
		Route:       "api.myapp",
		Method:      "POST",
		RequestPath: "/api/webhooks/github",
		Status:      200,
	}

	var text bytes.Buffer
	if err := writeLogEntry(&text, entry, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"12:41:03", "public", "api.myapp", "POST", "/api/webhooks/github", "200", "38ms", entry.ID} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text output %q missing %q", text.String(), want)
		}
	}

	var jsonOutput bytes.Buffer
	if err := writeLogEntry(&jsonOutput, entry, true); err != nil {
		t.Fatal(err)
	}
	var decoded logs.Entry
	if err := json.NewDecoder(&jsonOutput).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != entry.ID || decoded.Source != entry.Source || decoded.RequestPath != entry.RequestPath {
		t.Fatalf("json entry = %#v, want %#v", decoded, entry)
	}
}

func TestWriteLogHeader(t *testing.T) {
	var output bytes.Buffer
	if err := writeLogHeader(&output); err != nil {
		t.Fatal(err)
	}
	const want = "TIME     SOURCE ROUTE                METHOD  PATH STATUS DURATION ID\n"
	if output.String() != want {
		t.Fatalf("header = %q, want %q", output.String(), want)
	}
}

func TestWriteInspectEntry(t *testing.T) {
	entry := logs.Entry{
		ID:          "req_Ap7kQ3mN8vR2xLzC",
		Source:      logs.SourcePublic,
		Route:       "api.myapp",
		Host:        "api-myapp.routeup.dev",
		Method:      "POST",
		RequestPath: "/webhooks/github?event=push",
		Status:      202,
		Target:      route.Target{Path: "/", Port: 8080},
		Capture: &logs.Capture{Request: logs.CapturedMessage{
			Headers:  http.Header{"X-Webhook": {"original"}},
			Body:     []byte("payload"),
			Complete: true,
		}},
	}

	var output bytes.Buffer
	if err := writeInspectEntry(&output, entry); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{entry.ID, "Source: public", "Route: api.myapp", "Target: /:8080", "POST /webhooks/github?event=push", "Host: api-myapp.routeup.dev", "X-Webhook: original", "payload"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("inspect output %q missing %q", output.String(), want)
		}
	}
}
