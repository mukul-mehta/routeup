package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
	const want = "TIME      ROUTE                         SOURCE  STATUS  ID                    METHOD   DURATION  PATH\n"
	if output.String() != want {
		t.Fatalf("header = %q, want %q", output.String(), want)
	}
}

func TestWriteLogEntryKeepsColumnsAlignedForLongRoutes(t *testing.T) {
	var header bytes.Buffer
	if err := writeLogHeader(&header); err != nil {
		t.Fatal(err)
	}
	entry := logs.Entry{
		ID: "req_Ap7kQ3mN8vR2xLzC", StartedAt: time.Now(), Duration: 750 * time.Microsecond,
		Source: logs.SourceLocal, Route: "a-route-name-that-is-far-too-long.example-app",
		Method: http.MethodGet, RequestPath: "/routeup.json", Status: http.StatusOK,
	}
	var output bytes.Buffer
	if err := writeLogEntry(&output, entry, false); err != nil {
		t.Fatal(err)
	}
	sourceColumn := strings.Index(header.String(), "SOURCE")
	if got := strings.Index(output.String(), "local"); got != sourceColumn {
		t.Fatalf("source column starts at %d, want %d\nheader: %q\nentry:  %q", got, sourceColumn, header.String(), output.String())
	}
	if !strings.Contains(output.String(), "a-route-name-that-is-far-...") {
		t.Fatalf("long route was not clipped predictably: %q", output.String())
	}
}

func TestFormatLogDurationPreservesSubMillisecondPrecision(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{0, "0ms"},
		{500 * time.Nanosecond, "<1us"},
		{432 * time.Microsecond, "432us"},
		{999*time.Microsecond + 999*time.Nanosecond, "999us"},
		{time.Millisecond, "1ms"},
	}
	for _, test := range tests {
		if got := formatLogDuration(test.duration); got != test.want {
			t.Errorf("formatLogDuration(%s) = %q, want %q", test.duration, got, test.want)
		}
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
		Capture: &logs.Capture{Request: &logs.CapturedMessage{
			Headers:         http.Header{"X-Webhook": {"original"}},
			RedactedHeaders: []string{"authorization"},
			Body:            []byte("payload"),
			Complete:        true,
		}},
	}

	var output bytes.Buffer
	if err := writeInspectEntry(&output, entry, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{entry.ID, "Source: public", "Route: api.myapp", "Target: /:8080", "Method: POST", "Path: /webhooks/github?event=push", "Host: api-myapp.routeup.dev", "Redacted: authorization", "X-Webhook: original", "Body bytes: 7", "payload"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("inspect output %q missing %q", output.String(), want)
		}
	}
}

func TestWriteInspectEntryEscapesUntrustedBytes(t *testing.T) {
	entry := logs.Entry{
		ID:          "req_escape",
		Source:      logs.SourcePublic,
		Route:       "api.myapp",
		RequestPath: "/hook\x1b[2J",
		Capture: &logs.Capture{Request: &logs.CapturedMessage{
			Headers:  http.Header{"X-Test": {"value\x1b]52;bad\a"}},
			Body:     []byte{'o', 'k', '\n', 0, 0x1b, 0xff},
			Complete: true,
		}},
	}

	var safe bytes.Buffer
	if err := writeInspectEntry(&safe, entry, false); err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsAny(safe.Bytes(), "\x00\x1b\a\r\t") {
		t.Fatalf("safe output contains control bytes: %q", safe.Bytes())
	}
	for _, want := range []string{`/hook\x1b[2J`, `value\x1b]52;bad\x07`, `ok\n\x00\x1b\xff`} {
		if !strings.Contains(safe.String(), want) {
			t.Fatalf("safe output %q missing %q", safe.String(), want)
		}
	}

	var raw bytes.Buffer
	if err := writeInspectEntry(&raw, entry, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw.Bytes(), entry.Capture.Request.Body) {
		t.Fatalf("raw output does not contain body %q: %q", entry.Capture.Request.Body, raw.Bytes())
	}
}

func TestInspectFlagsAreMutuallyExclusive(t *testing.T) {
	cmd := newInspectCmd()
	cmd.SetArgs([]string{"req_test", "--raw", "--json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "if any flags in the group") {
		t.Fatalf("error = %v, want mutually exclusive flags error", err)
	}
}

func TestInspectCompletionReturnsCapturedIDs(t *testing.T) {
	entries := []logs.Entry{{ID: "req_captured", Captured: true}, {ID: "req_plain"}}
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": entries})
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	cmd := newInspectCmd()
	ids, directive := cmd.ValidArgsFunction(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp || len(ids) != 1 || ids[0] != "req_captured" {
		t.Fatalf("completion = %#v, %v", ids, directive)
	}
}

func TestParseLogSince(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	since, err := parseLogSince("10m", now)
	if err != nil || !since.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("parseLogSince = %v, %v", since, err)
	}
	if _, err := parseLogSince("later", now); err == nil {
		t.Fatal("invalid since value accepted")
	}
}

func TestTerminalStylesCanBeDisabled(t *testing.T) {
	plain := terminalStyles{}.accent("routeup")
	if plain != "routeup" {
		t.Fatalf("plain style = %q", plain)
	}
}

func TestPrintErrorKeepsRedirectedOutputPlain(t *testing.T) {
	var out bytes.Buffer
	PrintError(&out, errors.New("route failed\n  hint: try another name"))
	if got, want := out.String(), "route failed\n  hint: try another name\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestPrintErrorEscapesTTYOutputWithoutColor(t *testing.T) {
	var out bytes.Buffer
	printError(&out, errors.New("route \x1b[2J failed\n  hint: retry"), terminalStyles{tty: true})
	if strings.Contains(out.String(), "\x1b") {
		t.Fatalf("output contains terminal control: %q", out.String())
	}
	if !strings.Contains(out.String(), `route \x1b[2J failed`) {
		t.Fatalf("output lost escaped context: %q", out.String())
	}
}
