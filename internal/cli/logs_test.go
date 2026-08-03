package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/logs"
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
