package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/route"
)

func TestDashboardRequiresInteractiveTerminal(t *testing.T) {
	_, _, err := runRoot(t, "dashboard")
	if err == nil || !strings.Contains(err.Error(), "requires an interactive terminal") {
		t.Fatalf("error = %v", err)
	}
}

func TestDashboardIgnoresZeroSizedStartupResize(t *testing.T) {
	m := testDashboardModel()
	updated, _ := m.Update(tea.WindowSizeMsg{})
	m = updated.(dashboardModel)
	if m.width != 100 || m.height != 24 {
		t.Fatalf("size = %dx%d, want default 100x24", m.width, m.height)
	}
	if m.logs.opts.Limit != followLogLimit {
		t.Fatalf("log subscription limit = %d, want %d", m.logs.opts.Limit, followLogLimit)
	}
}

func TestDashboardResizeClampsSectionOffsets(t *testing.T) {
	m := testDashboardModel()
	m.routes = make([]ipc.Claim, 10)
	m.exposures = make([]ipc.ExposureStatus, 8)
	m.routeOffset, m.exposureOffset = 7, 6
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(dashboardModel)
	routeLimit, exposureLimit := m.dashboardSectionLimits()
	wantRouteOffset := max(0, len(m.routes)-routeLimit)
	wantExposureOffset := max(0, len(m.exposures)-exposureLimit)
	if m.routeOffset != wantRouteOffset || m.exposureOffset != wantExposureOffset {
		t.Fatalf("offsets = %d/%d, want %d/%d (limits = %d/%d)", m.routeOffset, m.exposureOffset, wantRouteOffset, wantExposureOffset, routeLimit, exposureLimit)
	}
}

func TestDashboardDuplicateReplayDoesNotMoveSelection(t *testing.T) {
	m := testDashboardModel()
	for index := 0; index < followLogLimit; index++ {
		m.logs.add(logs.Entry{ID: fmt.Sprintf("req_%03d", index)})
	}
	m.cursor = 100
	updated, _ := m.Update(followLogEntryMsg{entry: logs.Entry{ID: "req_150"}})
	m = updated.(dashboardModel)
	if m.cursor != 100 {
		t.Fatalf("cursor = %d, want 100 after duplicate replay", m.cursor)
	}
}

func TestDashboardModelShowsRoutesExposuresAndRequests(t *testing.T) {
	m := testDashboardModel()
	now := time.Now()
	updated, _ := m.Update(dashboardSnapshotMsg{
		online: true,
		status: ipc.Status{Version: "v1.0.0", UptimeSeconds: 90, Exposures: []ipc.ExposureStatus{{
			Route: "standalone", Host: "standalone.try.routeup.dev", Paths: []string{"/api/*"}, OwnerPID: 42, State: ipc.ExposureConnected,
		}}},
		routes: []ipc.Claim{{Name: "myapp", Targets: []route.Target{{Path: "/", Port: 3000}}, RegisteredAt: now.Add(-time.Minute)}},
	})
	m = updated.(dashboardModel)
	updated, _ = m.Update(followLogStateMsg{state: "connected", bootID: "boot"})
	m = updated.(dashboardModel)
	updated, _ = m.Update(followLogEntryMsg{entry: logs.Entry{
		ID: "req_1234567890abcdef", StartedAt: now, Source: logs.SourcePublic,
		Route: "standalone", Method: "POST", RequestPath: "/api/hook", Status: 202,
	}})
	m = updated.(dashboardModel)

	// All sections are always visible in the single-screen layout.
	// Use a wide terminal so all columns (including paths) are rendered.
	m.width = 120
	view := m.View()
	for _, want := range []string{"connected", "v1.0.0", "myapp", "standalone.try.routeup.dev", "/api/*", "req_1234567890abcdef", "POST"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestDashboardViewIsSafeAndWidthBounded(t *testing.T) {
	m := testDashboardModel()
	m.logs.state = "reconnecting"
	m.logs.detail = "a long reconnect detail that should be clipped"
	m.status = ipc.Status{Version: "bad\x1b]0;title\a"}
	m.online = true
	m.exposures = []ipc.ExposureStatus{{
		Route: "myapp", Host: "bad\x1b[2J.example", State: ipc.ExposureReconnecting,
	}}
	m.logs.add(logs.Entry{
		ID: "req_1234567890abcdef", StartedAt: time.Now(), Source: logs.SourcePublic,
		Route: "myapp", Method: "GET", RequestPath: "/bad\x1b[2J", Status: 500,
	})

	for _, width := range []int{20, 50, 100} {
		m.width = width
		view := m.View()
		if strings.ContainsAny(view, "\x1b\a") {
			t.Fatalf("view contains terminal controls at width %d: %q", width, view)
		}
		for lineNumber, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d line %d rendered at %d: %q", width, lineNumber+1, got, line)
			}
		}
	}
}

func TestDashboardInspectsCapturedRequest(t *testing.T) {
	full := logs.Entry{
		ID: "req_1234567890abcdef", Captured: true, Route: "myapp", Method: "POST", RequestPath: "/hook", Status: 202,
		Capture: &logs.Capture{Request: &logs.CapturedMessage{Body: []byte("payload\x1b[2J"), Complete: true}},
	}
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != ipc.PathRequests+"/"+full.ID {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(full)
	}))
	client := agentctl.NewClient(socketPath, "", "")
	m := newDashboardModel(client, context.Background(), make(chan tea.Msg), make(chan tea.Msg), func() {}, terminalStyles{}, 443)
	m.logs.add(logs.Entry{ID: full.ID, Captured: true})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not request inspection")
	}
	m = updated.(dashboardModel)
	updated, _ = m.Update(cmd())
	m = updated.(dashboardModel)
	if m.detail == nil {
		t.Fatal("captured request did not open detail view")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = updated.(dashboardModel)
	view := m.View()
	if !strings.Contains(view, `payload\x1b[2J`) || strings.Contains(view, "\x1b[2J") {
		t.Fatalf("unsafe or missing captured body: %q", view)
	}
	m.width = 20
	for lineNumber, line := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("detail line %d rendered at %d: %q", lineNumber+1, got, line)
		}
	}
}

func TestDashboardSerializesInspectRequests(t *testing.T) {
	m := testDashboardModel()
	m.logs.add(logs.Entry{ID: "req_first", Captured: true})
	m.logs.add(logs.Entry{ID: "req_second", Captured: true})

	updated, firstCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(dashboardModel)
	if firstCmd == nil || m.inspectingID != "req_first" {
		t.Fatalf("first inspect = %q, command = %v", m.inspectingID, firstCmd)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(dashboardModel)
	updated, secondCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(dashboardModel)
	if secondCmd != nil || m.inspectingID != "req_first" {
		t.Fatalf("overlapping inspect started: id=%q command=%v", m.inspectingID, secondCmd)
	}

	updated, _ = m.Update(dashboardInspectMsg{id: "req_stale", entry: logs.Entry{ID: "req_stale"}})
	m = updated.(dashboardModel)
	if m.detail != nil || m.inspectingID != "req_first" {
		t.Fatal("stale inspect response changed dashboard state")
	}
}

func TestDashboardWrapsLongCapturedValues(t *testing.T) {
	body := strings.Repeat("a", 120) + "  END"
	entry := logs.Entry{
		ID: "req_long", Captured: true,
		Capture: &logs.Capture{Request: &logs.CapturedMessage{Body: []byte(body), Complete: true}},
	}
	m := testDashboardModel()
	m.width, m.height, m.detail = 20, 10, &entry
	lines, err := dashboardDetailLines(entry, m.styles, m.width)
	if err != nil {
		t.Fatal(err)
	}
	target := -1
	targetLine := ""
	for index, line := range lines {
		if strings.Contains(line, "END") {
			target = index
			targetLine = line
			break
		}
	}
	if target < 0 {
		t.Fatal("wrapped detail lost the end of a long body")
	}
	if !strings.HasPrefix(targetLine, "  ") {
		t.Fatalf("wrapped detail did not preserve leading spaces: %q", targetLine)
	}
	m.detailScroll = min(target, m.detailMaxScroll())
	if !strings.Contains(m.View(), "END") {
		t.Fatalf("long body suffix is not reachable:\n%s", m.View())
	}
}

func TestDashboardMarksRowsOutsidePreview(t *testing.T) {
	m := testDashboardModel()
	m.height = 24
	for index := 0; index < 20; index++ {
		m.routes = append(m.routes, ipc.Claim{Name: fmt.Sprintf("route-%02d", index)})
	}
	for index := 0; index < 10; index++ {
		m.exposures = append(m.exposures, ipc.ExposureStatus{
			Route: fmt.Sprintf("route-%02d", index),
			Host:  fmt.Sprintf("route-%02d.example", index),
		})
	}

	// Routes section shows a paginated label when more rows exist than fit on screen.
	view := m.View()
	if !strings.Contains(view, "of 20") {
		t.Fatalf("expected paginated routes label, view:\n%s", view)
	}

	// G scrolls routes to the last visible page when routes section is focused.
	m.focus = dashboardFocusRoutes
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = updated.(dashboardModel)
	if view = m.View(); !strings.Contains(view, "route-19") {
		t.Fatalf("last route not reachable after G:\n%s", view)
	}

	// Shift+Tab moves focus backward (Routes → Requests).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(dashboardModel)
	if m.focus != dashboardFocusRequests {
		t.Fatalf("shift-tab focus = %d, want requests (%d)", m.focus, dashboardFocusRequests)
	}

	// Tab cycles focus forward (Requests → Routes).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(dashboardModel)
	if m.focus != dashboardFocusRoutes {
		t.Fatalf("tab focus = %d, want routes (%d)", m.focus, dashboardFocusRoutes)
	}
}

func TestDashboardKeepsFooterAtConstrainedHeights(t *testing.T) {
	m := testDashboardModel()
	for _, height := range []int{3, 18, 22} {
		m.height = height
		view := m.View()
		if !strings.Contains(view, "q quit") {
			t.Fatalf("height %d hides controls:\n%s", height, view)
		}
		if lines := strings.Count(view, "\n") + 1; lines > height {
			t.Fatalf("height %d rendered %d lines", height, lines)
		}
	}
	m.height = 18
	m.inspectErr = "request was not captured"
	if !strings.Contains(m.View(), m.inspectErr) {
		t.Fatalf("inspect error hidden:\n%s", m.View())
	}
}

func TestFetchDashboardSnapshotIncludesAllExposureOwners(t *testing.T) {
	wantStatus := ipc.Status{BootID: "boot", Exposures: []ipc.ExposureStatus{{
		Route: "standalone", Host: "standalone.try.routeup.dev", OwnerPID: 99, State: ipc.ExposureConnected,
	}}}
	wantRoutes := []ipc.Claim{{Name: "myapp", OwnerPID: 42}}
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		switch r.URL.Path {
		case ipc.PathStatus:
			_ = json.NewEncoder(w).Encode(wantStatus)
		case ipc.PathRoutes:
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": wantRoutes})
		default:
			http.NotFound(w, r)
		}
	}))
	snapshot := fetchDashboardSnapshot(context.Background(), agentctl.NewClient(socketPath, "", ""))
	if !snapshot.online || len(snapshot.routes) != 1 || len(snapshot.status.Exposures) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.status.Exposures[0].OwnerPID == snapshot.routes[0].OwnerPID {
		t.Fatal("test did not cover a standalone exposure owner")
	}
}

func TestFetchDashboardSnapshotTreatsMissingAgentAsOffline(t *testing.T) {
	dir, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	client := agentctl.NewClient(filepath.Join(dir, "missing.sock"), "", "")
	snapshot := fetchDashboardSnapshot(context.Background(), client)
	if snapshot.online || snapshot.err != nil {
		t.Fatalf("snapshot = %#v, want clean offline state", snapshot)
	}
}

func testDashboardModel() dashboardModel {
	client := agentctl.NewClient("/tmp/routeup-dashboard-test-missing.sock", "", "")
	return newDashboardModel(client, context.Background(), make(chan tea.Msg), make(chan tea.Msg), func() {}, terminalStyles{}, 443)
}
