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
	lines := make([]string, 0, max(1, m.height))
	add := func(line string) {
		lines = append(lines, clipTerminalLine(line, width))
	}

	connection := m.styles.muted(m.logs.state)
	switch m.logs.state {
	case "connected":
		connection = m.styles.success(m.logs.state)
	case "waiting", "reconnecting":
		connection = m.styles.warning(m.logs.state)
	case "error":
		connection = m.styles.failure(m.logs.state)
	}
	add(joinAcross(m.styles.accent("routeup dashboard"), connection, width))
	add(m.dashboardSummary())
	if m.height > 0 && m.height < 7 {
		footer := m.dashboardFooter()
		compact := []string{clipTerminalLine(joinAcross(m.styles.accent("routeup dashboard"), connection, width), width)}
		if m.height >= 3 {
			compact = append(compact, clipTerminalLine(m.dashboardSummary(), width))
		}
		if m.height >= 2 {
			compact = append(compact, clipTerminalLine(footer, width))
		}
		return strings.Join(compact[:min(m.height, len(compact))], "\n")
	}
	add("")

	if m.height >= 22 {
		routeLimit, exposureLimit := m.dashboardSectionLimits()
		m.appendRouteLines(&lines, width, routeLimit)
		lines = append(lines, "")
		m.appendExposureLines(&lines, width, exposureLimit)
		lines = append(lines, "")
	}

	addTo := func(line string) { lines = append(lines, clipTerminalLine(line, width)) }
	requestTitle := fmt.Sprintf("REQUESTS (%d)", len(m.logs.entries))
	if m.focus == dashboardFocusRequests {
		requestTitle = m.styles.accent("> " + requestTitle)
	} else {
		requestTitle = m.styles.label(requestTitle)
	}
	addTo(requestTitle)
	requestWidth := max(1, width-2)
	addTo("  " + formatFollowLogHeader(requestWidth, m.styles))
	available := max(0, m.height-len(lines)-2)
	if len(m.logs.entries) == 0 && available > 0 {
		addTo("  " + m.styles.muted("Waiting for requests..."))
	} else if available > 0 {
		start, end := dashboardWindow(len(m.logs.entries), m.cursor, available)
		for index := start; index < end; index++ {
			marker := "  "
			if index == m.cursor {
				marker = m.styles.accent("> ")
			}
			addTo(marker + formatFollowLogEntry(m.logs.entries[index], requestWidth, m.styles))
		}
	}

	lines = append(lines, "")
	addTo(m.dashboardFooter())
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) dashboardFooter() string {
	if m.inspectErr != "" {
		return m.styles.warning(terminalEscapeString(m.inspectErr))
	}
	if m.inspectingID != "" {
		return m.styles.muted("loading request " + terminalEscapeString(m.inspectingID) + "...")
	}
	if m.height < 22 {
		return m.styles.muted("j/k select  enter inspect  g/G first/last  q quit")
	}
	return m.styles.muted("tab section  j/k navigate  enter inspect  g/G first/last  q quit")
}

func (m dashboardModel) dashboardSectionLimits() (int, int) {
	extra := max(0, m.height-24) / 4
	return 3 + extra, 2 + extra
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
		parts = append(parts, "agent not running; dashboard is read-only")
	}
	if m.snapshotErr != nil {
		parts = append(parts, terminalEscapeString(m.snapshotErr.Error()))
	} else if m.logs.detail != "" {
		parts = append(parts, terminalEscapeString(m.logs.detail))
	}
	return m.styles.muted(strings.Join(parts, "  "))
}

func (m dashboardModel) appendRouteLines(lines *[]string, width, limit int) {
	start := min(m.routeOffset, max(0, len(m.routes)-limit))
	end := min(len(m.routes), start+limit)
	*lines = append(*lines, clipTerminalLine(m.dashboardSectionTitle("ROUTES", start, end, len(m.routes), m.focus == dashboardFocusRoutes), width))
	*lines = append(*lines, clipTerminalLine(formatDashboardRoute(ipc.Claim{Name: "NAME", OwnerCWD: "LOCAL"}, width, m.styles, m.tlsPort, true), width))
	if len(m.routes) == 0 {
		*lines = append(*lines, clipTerminalLine(m.styles.muted("No active local routes"), width))
		return
	}
	for _, claim := range m.routes[start:end] {
		*lines = append(*lines, clipTerminalLine(formatDashboardRoute(claim, width, m.styles, m.tlsPort, false), width))
	}
}

func (m dashboardModel) appendExposureLines(lines *[]string, width, limit int) {
	start := min(m.exposureOffset, max(0, len(m.exposures)-limit))
	end := min(len(m.exposures), start+limit)
	*lines = append(*lines, clipTerminalLine(m.dashboardSectionTitle("PUBLIC EXPOSURES", start, end, len(m.exposures), m.focus == dashboardFocusExposures), width))
	*lines = append(*lines, clipTerminalLine(formatDashboardExposure(ipc.ExposureStatus{State: "STATE", Route: "ROUTE", Host: "PUBLIC URL"}, width, m.styles, true), width))
	if len(m.exposures) == 0 {
		*lines = append(*lines, clipTerminalLine(m.styles.muted("No active public exposures"), width))
		return
	}
	for _, exposure := range m.exposures[start:end] {
		*lines = append(*lines, clipTerminalLine(formatDashboardExposure(exposure, width, m.styles, false), width))
	}
}

func (m dashboardModel) dashboardSectionTitle(label string, start, end, total int, focused bool) string {
	count := fmt.Sprintf("%d", total)
	if total > 0 && (start > 0 || end < total) {
		count = fmt.Sprintf("%d-%d of %d", start+1, end, total)
	}
	title := fmt.Sprintf("%s (%s)", label, count)
	if focused {
		return m.styles.accent("> " + title)
	}
	return m.styles.label(title)
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
