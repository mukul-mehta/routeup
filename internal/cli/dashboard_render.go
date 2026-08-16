package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
)

func (m dashboardModel) View() string {
	if m.detail != nil {
		return m.detailView()
	}
	width := max(1, m.width)
	connection := m.connectionBadge()
	header := joinAcross(m.styles.accent("routeup dashboard"), connection, width)

	if m.height > 0 && m.height < 7 {
		return m.compactView(header, width)
	}

	footer := clipTerminalLine(m.dashboardFooter(), width)
	routeLimit, exposureLimit := m.dashboardSectionLimits()

	lines := []string{
		header,
		clipTerminalLine(m.dashboardSummary(), width),
		"",
	}

	// Routes section
	rStart := min(m.routeOffset, max(0, len(m.routes)-routeLimit))
	rEnd := min(len(m.routes), rStart+routeLimit)
	lines = append(lines, m.dashboardSectionTitle("ROUTES", rStart, rEnd, len(m.routes), m.focus == dashboardFocusRoutes))
	lines = append(lines, clipTerminalLine(formatDashboardRoute(ipc.Claim{Name: "NAME"}, width, m.styles, m.tlsPort, true), width))
	if len(m.routes) == 0 {
		lines = append(lines, clipTerminalLine("  "+m.styles.muted("No active local routes"), width))
	} else {
		for _, claim := range m.routes[rStart:rEnd] {
			lines = append(lines, clipTerminalLine(formatDashboardRoute(claim, width, m.styles, m.tlsPort, false), width))
		}
	}

	lines = append(lines, "")

	// Exposures section
	eStart := min(m.exposureOffset, max(0, len(m.exposures)-exposureLimit))
	eEnd := min(len(m.exposures), eStart+exposureLimit)
	lines = append(lines, m.dashboardSectionTitle("PUBLIC EXPOSURES", eStart, eEnd, len(m.exposures), m.focus == dashboardFocusExposures))
	lines = append(lines, clipTerminalLine(formatDashboardExposure(ipc.ExposureStatus{State: "STATE", Route: "ROUTE", Host: "PUBLIC URL"}, width, m.styles, true), width))
	if len(m.exposures) == 0 {
		lines = append(lines, clipTerminalLine("  "+m.styles.muted("No active public exposures"), width))
	} else {
		for _, exposure := range m.exposures[eStart:eEnd] {
			lines = append(lines, clipTerminalLine(formatDashboardExposure(exposure, width, m.styles, false), width))
		}
	}

	lines = append(lines, "")

	// Requests section — fills remaining height above footer
	lines = append(lines, m.dashboardSectionTitle("REQUESTS", 0, len(m.logs.entries), len(m.logs.entries), m.focus == dashboardFocusRequests))
	lines = append(lines, clipTerminalLine("  "+formatFollowLogHeader(max(1, width-2), m.styles), width))
	available := max(0, m.height-len(lines)-2) // reserve blank + footer
	if len(m.logs.entries) == 0 && available > 0 {
		lines = append(lines, clipTerminalLine("  "+m.styles.muted("Waiting for requests..."), width))
	} else if available > 0 {
		start, end := dashboardWindow(len(m.logs.entries), m.cursor, available)
		for index := start; index < end; index++ {
			marker := "  "
			if index == m.cursor {
				marker = m.styles.accent("> ")
			}
			lines = append(lines, clipTerminalLine(marker+formatFollowLogEntry(m.logs.entries[index], max(1, width-2), m.styles), width))
		}
	}

	lines = append(lines, "")
	lines = append(lines, footer)
	// Ensure footer is always the last visible line.
	if m.height > 0 && len(lines) > m.height {
		lines = append(lines[:m.height-1], footer)
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) connectionBadge() string {
	switch m.logs.state {
	case "connected":
		return m.styles.success(m.logs.state)
	case "waiting", "reconnecting":
		return m.styles.warning(m.logs.state)
	case "error":
		return m.styles.failure(m.logs.state)
	default:
		return m.styles.muted(m.logs.state)
	}
}

func (m dashboardModel) compactView(header string, width int) string {
	lines := []string{clipTerminalLine(header, width)}
	if m.height >= 3 {
		lines = append(lines, clipTerminalLine(m.dashboardSummary(), width))
	}
	if m.height >= 2 {
		lines = append(lines, clipTerminalLine(m.dashboardFooter(), width))
	}
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// sectionTitle renders a section header with a ─ fill line to the terminal edge.
// Focused sections use the accent style and a > marker; others use the label style.
func (m dashboardModel) sectionTitle(label string, focused bool) string {
	var prefix string
	if focused {
		prefix = m.styles.accent("> " + label + " ")
	} else {
		prefix = m.styles.label("  " + label + " ")
	}
	prefixWidth := ansi.StringWidth(prefix)
	width := max(1, m.width)
	if prefixWidth >= width {
		return clipTerminalLine(prefix, width)
	}
	return prefix + m.styles.muted(strings.Repeat("─", width-prefixWidth))
}

func (m dashboardModel) dashboardSectionTitle(label string, start, end, total int, focused bool) string {
	count := fmt.Sprintf("%d", total)
	if total > 0 && (start > 0 || end < total) {
		count = fmt.Sprintf("%d-%d of %d", start+1, end, total)
	}
	return m.sectionTitle(fmt.Sprintf("%s (%s)", label, count), focused)
}

func (m dashboardModel) dashboardFooter() string {
	if m.inspectErr != "" {
		return m.styles.warning(terminalEscapeString(m.inspectErr))
	}
	if m.inspectingID != "" {
		return m.styles.muted("loading request " + terminalEscapeString(m.inspectingID) + "...")
	}
	return m.styles.muted("tab focus  j/k navigate  enter inspect  g/G first/last  q quit")
}

func (m dashboardModel) dashboardSummary() string {
	parts := []string{
		pluralCount(len(m.routes), "route"),
		pluralCount(len(m.exposures), "exposure"),
		pluralCount(len(m.logs.entries), "request"),
	}
	if m.online {
		parts = append(parts, fmt.Sprintf("agent %s up %s", terminalEscapeString(m.status.Version), humanDuration(time.Duration(m.status.UptimeSeconds)*time.Second)))
	} else {
		parts = append(parts, "agent not running")
	}
	if m.snapshotErr != nil {
		parts = append(parts, terminalEscapeString(m.snapshotErr.Error()))
	} else if m.logs.detail != "" {
		parts = append(parts, terminalEscapeString(m.logs.detail))
	}
	return m.styles.muted(strings.Join(parts, "  "))
}

func (m dashboardModel) detailView() string {
	width := max(1, m.width)
	reportLines, err := dashboardDetailLines(*m.detail, m.styles, width)
	if err != nil {
		return clipTerminalLine(m.styles.failure(terminalEscapeString(err.Error())), width)
	}
	if m.height > 0 && m.height < 4 {
		lines := []string{joinAcross(m.styles.accent("request "+terminalEscapeString(m.detail.ID)), m.styles.muted("esc back"), width)}
		if m.height >= 2 {
			lines = append(lines, clipTerminalLine(m.styles.muted("esc back  q quit"), width))
		}
		return strings.Join(lines[:min(m.height, len(lines))], "\n")
	}
	available := max(1, m.height-3)
	start := min(m.detailScroll, max(0, len(reportLines)-available))
	end := min(len(reportLines), start+available)
	lines := []string{
		joinAcross(m.styles.accent("request "+terminalEscapeString(m.detail.ID)), m.styles.muted("esc back"), width),
		"",
	}
	for _, line := range reportLines[start:end] {
		lines = append(lines, clipTerminalLine(line, width))
	}
	lines = append(lines, clipTerminalLine(m.styles.muted("j/k scroll  g/G first/last  esc back  q quit"), width))
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) detailMaxScroll() int {
	if m.detail == nil {
		return 0
	}
	reportLines, err := dashboardDetailLines(*m.detail, m.styles, max(1, m.width))
	if err != nil {
		return 0
	}
	return max(0, len(reportLines)-max(1, m.height-3))
}

func dashboardDetailLines(entry logs.Entry, styles terminalStyles, width int) ([]string, error) {
	var report strings.Builder
	if err := writeInspectEntryStyled(&report, entry, false, styles); err != nil {
		return nil, err
	}
	lines := make([]string, 0, strings.Count(report.String(), "\n")+1)
	for _, line := range strings.Split(strings.TrimSuffix(report.String(), "\n"), "\n") {
		wrapped := ansi.Hardwrap(line, max(1, width), true)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines, nil
}
