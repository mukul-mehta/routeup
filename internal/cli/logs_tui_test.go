package cli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mukul-mehta/routeup/internal/logs"
)

func TestFollowLogsModelBoundsAndDeduplicatesEntries(t *testing.T) {
	m := newFollowLogsModel(make(chan tea.Msg), func() {}, logs.ListOptions{}, terminalStyles{})
	for i := 0; i < followLogLimit+5; i++ {
		m.add(logs.Entry{ID: fmt.Sprintf("req_%03d", i)})
	}
	m.add(logs.Entry{ID: "req_204"})

	if len(m.entries) != followLogLimit {
		t.Fatalf("entries = %d, want %d", len(m.entries), followLogLimit)
	}
	if m.entries[0].ID != "req_005" || m.entries[len(m.entries)-1].ID != "req_204" {
		t.Fatalf("bounded entries = %q...%q", m.entries[0].ID, m.entries[len(m.entries)-1].ID)
	}
}

func TestFollowLogsModelResetsHistoryAfterAgentRestart(t *testing.T) {
	m := newFollowLogsModel(make(chan tea.Msg), func() {}, logs.ListOptions{}, terminalStyles{})
	m.bootID = "boot-one"
	m.add(logs.Entry{ID: "req_old"})

	updated, _ := m.Update(followLogStateMsg{state: "connected", bootID: "boot-two"})
	m = updated.(followLogsModel)
	if len(m.entries) != 0 || len(m.seen) != 0 {
		t.Fatalf("history survived boot change: %#v", m.entries)
	}
	if !strings.Contains(m.detail, "history reset") {
		t.Fatalf("detail = %q, want reset notice", m.detail)
	}
}

func TestFollowLogsModelQuitCancelsStream(t *testing.T) {
	cancelled := false
	m := newFollowLogsModel(make(chan tea.Msg), func() { cancelled = true }, logs.ListOptions{}, terminalStyles{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !cancelled {
		t.Fatal("quit did not cancel the log stream")
	}
	if cmd == nil || cmd() == nil {
		t.Fatal("quit did not return a Bubble Tea quit command")
	}
}

func TestFollowLogsViewEscapesUntrustedValues(t *testing.T) {
	m := newFollowLogsModel(make(chan tea.Msg), func() {}, logs.ListOptions{}, terminalStyles{})
	m.state = "connected"
	m.add(logs.Entry{
		ID:          "req_safe",
		StartedAt:   time.Date(2026, time.August, 12, 12, 0, 0, 0, time.Local),
		Source:      logs.SourcePublic,
		Route:       "myapp\x1b[2J",
		Method:      "GET",
		RequestPath: "/\x1b]0;bad\a",
		Status:      200,
	})

	view := m.View()
	if strings.ContainsAny(view, "\x1b\a") {
		t.Fatalf("view contains terminal controls: %q", view)
	}
	if !strings.Contains(view, `\x1b`) || !strings.Contains(view, `\x07`) {
		t.Fatalf("view does not preserve escaped context: %q", view)
	}
}

func TestFollowLogRowsFitTerminalWidth(t *testing.T) {
	entry := logs.Entry{
		ID: "req_1234567890abcdef", StartedAt: time.Now(), Source: logs.SourcePublic,
		Route: "api.myapp", Method: "POST", RequestPath: "/a/very/long/webhook/request/path", Status: 202,
	}
	for _, width := range []int{50, 80, 120} {
		row := formatFollowLogEntry(entry, width, terminalStyles{})
		if got := lipgloss.Width(row); got != width {
			t.Errorf("width %d row rendered at %d: %q", width, got, row)
		}
	}
}

func TestFollowLogsViewFitsNarrowTerminals(t *testing.T) {
	m := newFollowLogsModel(make(chan tea.Msg), func() {}, logs.ListOptions{
		Route: "a-very-long-route-name-that-must-be-truncated",
	}, terminalStyles{})
	m.state = "reconnecting"
	m.detail = "a deliberately long reconnect detail that must not wrap"
	m.add(logs.Entry{
		ID: "req_1234567890abcdef", StartedAt: time.Now(), Source: logs.SourcePublic,
		Route: "api.myapp", Method: "POST", RequestPath: "/a/very/long/webhook/request/path", Status: 202,
	})
	for _, width := range []int{20, 50, 80, 120} {
		m.width = width
		for lineNumber, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d line %d rendered at %d: %q", width, lineNumber+1, got, line)
			}
		}
	}
}

func TestFollowLogsModelQuitsOnPermanentStreamError(t *testing.T) {
	want := errors.New("bad content type")
	m := newFollowLogsModel(make(chan tea.Msg), func() {}, logs.ListOptions{}, terminalStyles{})
	updated, cmd := m.Update(followLogStateMsg{state: "error", detail: "live log stream failed", err: want})
	m = updated.(followLogsModel)
	if !errors.Is(m.err, want) || cmd == nil || cmd() == nil {
		t.Fatalf("error = %v, command = %v", m.err, cmd)
	}
	if isTransientFollowError(want) {
		t.Fatal("permanent protocol error classified as transient")
	}
}

func TestFollowLogsClassifiesAgentDisconnectsAsTransient(t *testing.T) {
	for _, err := range []error{syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE} {
		if !isTransientFollowError(fmt.Errorf("agent request: %w", err)) {
			t.Errorf("%v was not classified as transient", err)
		}
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
