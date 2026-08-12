package cli

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
)

type dashboardModel struct {
	client         *agentctl.Client
	ctx            context.Context
	snapshotEvents <-chan tea.Msg
	logs           followLogsModel
	styles         terminalStyles
	tlsPort        int
	status         ipc.Status
	routes         []ipc.Claim
	exposures      []ipc.ExposureStatus
	online         bool
	snapshotErr    error
	width          int
	height         int
	focus          dashboardFocus
	routeOffset    int
	exposureOffset int
	cursor         int
	detail         *logs.Entry
	detailScroll   int
	inspectingID   string
	inspectErr     string
}

type dashboardFocus int

const (
	dashboardFocusRequests dashboardFocus = iota
	dashboardFocusRoutes
	dashboardFocusExposures
)

func newDashboardModel(client *agentctl.Client, ctx context.Context, logEvents, snapshotEvents <-chan tea.Msg, cancel context.CancelFunc, styles terminalStyles, tlsPort int) dashboardModel {
	return dashboardModel{
		client:         client,
		ctx:            ctx,
		snapshotEvents: snapshotEvents,
		logs:           newFollowLogsModel(logEvents, cancel, dashboardLogOptions(), styles),
		styles:         styles,
		tlsPort:        tlsPort,
		width:          100,
		height:         24,
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(m.logs.Init(), waitForDashboardSnapshot(m.snapshotEvents))
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width, m.logs.width = msg.Width, msg.Width
		}
		if msg.Height > 0 {
			m.height, m.logs.height = msg.Height, msg.Height
		}
		if m.height < 22 {
			m.focus = dashboardFocusRequests
		}
		routeLimit, exposureLimit := m.dashboardSectionLimits()
		m.routeOffset = min(m.routeOffset, max(0, len(m.routes)-routeLimit))
		m.exposureOffset = min(m.exposureOffset, max(0, len(m.exposures)-exposureLimit))
		m.detailScroll = min(m.detailScroll, m.detailMaxScroll())
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	case followLogEntryMsg:
		wasLatest := len(m.logs.entries) == 0 || m.cursor == len(m.logs.entries)-1
		wasFull := len(m.logs.entries) == followLogLimit
		if !m.logs.add(msg.entry) {
			return m, waitForFollowLogEvent(m.logs.events)
		}
		if wasLatest {
			m.cursor = max(0, len(m.logs.entries)-1)
		} else if wasFull {
			m.cursor = max(0, m.cursor-1)
		}
		return m, waitForFollowLogEvent(m.logs.events)
	case followLogStateMsg:
		previousBootID := m.logs.bootID
		updated, cmd := m.logs.Update(msg)
		m.logs = updated.(followLogsModel)
		if previousBootID != "" && m.logs.bootID != previousBootID {
			m.cursor = 0
			m.detail = nil
			m.inspectingID = ""
		}
		return m, cmd
	case followLogClosedMsg:
		return m, nil
	case dashboardSnapshotMsg:
		m.online = msg.online
		m.snapshotErr = msg.err
		if msg.online {
			m.status = msg.status
			m.routes = append([]ipc.Claim(nil), msg.routes...)
			m.exposures = append([]ipc.ExposureStatus(nil), msg.status.Exposures...)
			routeLimit, exposureLimit := m.dashboardSectionLimits()
			m.routeOffset = min(m.routeOffset, max(0, len(m.routes)-routeLimit))
			m.exposureOffset = min(m.exposureOffset, max(0, len(m.exposures)-exposureLimit))
		} else {
			m.status = ipc.Status{}
			m.routes = nil
			m.exposures = nil
		}
		return m, waitForDashboardSnapshot(m.snapshotEvents)
	case dashboardSnapshotClosedMsg:
		return m, nil
	case dashboardInspectMsg:
		if msg.id != m.inspectingID {
			return m, nil
		}
		m.inspectingID = ""
		if msg.err != nil {
			m.inspectErr = msg.err.Error()
			return m, nil
		}
		m.inspectErr = ""
		m.detail = &msg.entry
		m.detailScroll = 0
		return m, nil
	default:
		return m, nil
	}
}

func (m dashboardModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "q" || key == "ctrl+c" {
		m.logs.cancel()
		return m, tea.Quit
	}
	if m.detail != nil {
		switch key {
		case "esc", "backspace":
			m.detail = nil
			m.detailScroll = 0
		case "up", "k":
			m.detailScroll = max(0, m.detailScroll-1)
		case "down", "j":
			m.detailScroll = min(m.detailScroll+1, m.detailMaxScroll())
		case "g", "home":
			m.detailScroll = 0
		case "G", "end":
			m.detailScroll = m.detailMaxScroll()
		}
		return m, nil
	}

	m.inspectErr = ""
	if key == "tab" {
		if m.height >= 22 {
			m.focus = (m.focus + 1) % 3
		}
		return m, nil
	}
	if key == "shift+tab" {
		if m.height >= 22 {
			m.focus = (m.focus + 2) % 3
		}
		return m, nil
	}
	routeLimit, exposureLimit := m.dashboardSectionLimits()
	if m.focus == dashboardFocusRoutes {
		switch key {
		case "up", "k":
			m.routeOffset = max(0, m.routeOffset-1)
		case "down", "j":
			m.routeOffset = min(max(0, len(m.routes)-routeLimit), m.routeOffset+1)
		case "g", "home":
			m.routeOffset = 0
		case "G", "end":
			m.routeOffset = max(0, len(m.routes)-routeLimit)
		}
		return m, nil
	}
	if m.focus == dashboardFocusExposures {
		switch key {
		case "up", "k":
			m.exposureOffset = max(0, m.exposureOffset-1)
		case "down", "j":
			m.exposureOffset = min(max(0, len(m.exposures)-exposureLimit), m.exposureOffset+1)
		case "g", "home":
			m.exposureOffset = 0
		case "G", "end":
			m.exposureOffset = max(0, len(m.exposures)-exposureLimit)
		}
		return m, nil
	}
	switch key {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(max(0, len(m.logs.entries)-1), m.cursor+1)
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(0, len(m.logs.entries)-1)
	case "enter":
		if len(m.logs.entries) == 0 || m.inspectingID != "" {
			return m, nil
		}
		entry := m.logs.entries[m.cursor]
		if !entry.Captured {
			m.inspectErr = "request was not captured; enable capture in routeup config"
			return m, nil
		}
		m.inspectingID = entry.ID
		return m, inspectDashboardRequest(m.ctx, m.client, entry.ID)
	}
	return m, nil
}
