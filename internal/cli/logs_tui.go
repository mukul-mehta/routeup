package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/logs"
)

const followLogLimit = 200

type followLogEntryMsg struct {
	entry logs.Entry
}

type followLogStateMsg struct {
	state  string
	detail string
	bootID string
	err    error
}

type followLogClosedMsg struct{}

type followLogsModel struct {
	events  <-chan tea.Msg
	cancel  context.CancelFunc
	opts    logs.ListOptions
	styles  terminalStyles
	entries []logs.Entry
	seen    map[string]struct{}
	width   int
	height  int
	scroll  int
	state   string
	detail  string
	bootID  string
	err     error
}

func newFollowLogsModel(events <-chan tea.Msg, cancel context.CancelFunc, opts logs.ListOptions, styles terminalStyles) followLogsModel {
	return followLogsModel{
		events: events,
		cancel: cancel,
		opts:   opts,
		styles: styles,
		seen:   make(map[string]struct{}),
		width:  100,
		height: 24,
		state:  "connecting",
	}
}

func (m followLogsModel) Init() tea.Cmd {
	return waitForFollowLogEvent(m.events)
}

func waitForFollowLogEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return followLogClosedMsg{}
		}
		return msg
	}
}

func (m followLogsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.scroll = min(m.scroll, m.maxScroll())
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "up", "k":
			m.scroll = min(m.scroll+1, m.maxScroll())
		case "down", "j":
			m.scroll = max(m.scroll-1, 0)
		case "g", "home":
			m.scroll = m.maxScroll()
		case "G", "end":
			m.scroll = 0
		}
		return m, nil
	case followLogEntryMsg:
		m.add(msg.entry)
		return m, waitForFollowLogEvent(m.events)
	case followLogStateMsg:
		if msg.bootID != "" && m.bootID != "" && msg.bootID != m.bootID {
			m.entries = nil
			m.seen = make(map[string]struct{})
			m.scroll = 0
			msg.detail = "agent restarted; request history reset"
		}
		if msg.bootID != "" {
			m.bootID = msg.bootID
		}
		m.state = msg.state
		m.detail = msg.detail
		m.err = msg.err
		if msg.err != nil {
			return m, tea.Quit
		}
		return m, waitForFollowLogEvent(m.events)
	case followLogClosedMsg:
		return m, nil
	default:
		return m, nil
	}
}

func (m *followLogsModel) add(entry logs.Entry) {
	if _, exists := m.seen[entry.ID]; exists {
		return
	}
	wasScrolled := m.scroll > 0
	if len(m.entries) == followLogLimit {
		delete(m.seen, m.entries[0].ID)
		m.entries = m.entries[1:]
	}
	m.entries = append(m.entries, entry)
	m.seen[entry.ID] = struct{}{}
	if wasScrolled {
		m.scroll = min(m.scroll+1, m.maxScroll())
	}
}

func (m followLogsModel) maxScroll() int {
	return max(0, len(m.entries)-m.visibleRows())
}

func (m followLogsModel) visibleRows() int {
	return max(1, m.height-7)
}

func (m followLogsModel) View() string {
	width := max(1, m.width)
	state := m.styles.muted(m.state)
	switch m.state {
	case "connected":
		state = m.styles.success(m.state)
	case "waiting", "reconnecting":
		state = m.styles.warning(m.state)
	case "error":
		state = m.styles.failure(m.state)
	}

	var view strings.Builder
	view.WriteString(joinAcross(m.styles.accent("routeup logs"), state, width))
	view.WriteByte('\n')
	detail := m.styles.muted(followLogFilterSummary(m.opts))
	if m.detail != "" {
		detail += m.styles.muted("  " + terminalEscapeString(m.detail))
	}
	view.WriteString(clipTerminalLine(detail, width))
	view.WriteString("\n\n")
	view.WriteString(clipTerminalLine(formatFollowLogHeader(width, m.styles), width))
	view.WriteByte('\n')

	visible := m.visibleRows()
	end := len(m.entries) - m.scroll
	start := max(0, end-visible)
	if start == end {
		view.WriteString(clipTerminalLine(m.styles.muted("Waiting for matching requests..."), width))
		view.WriteByte('\n')
	} else {
		for _, entry := range m.entries[start:end] {
			view.WriteString(clipTerminalLine(formatFollowLogEntry(entry, width, m.styles), width))
			view.WriteByte('\n')
		}
	}

	shown := fmt.Sprintf("%d-%d of %d", min(start+1, end), end, len(m.entries))
	if len(m.entries) == 0 {
		shown = "0 requests"
	}
	footer := m.styles.muted("up/k scroll  down/j scroll  g/G first/last  q quit")
	view.WriteByte('\n')
	view.WriteString(joinAcross(footer, m.styles.muted(shown), width))
	return view.String()
}

func runLogsTUI(cmd *cobra.Command, client *agentctl.Client, opts logs.ListOptions) error {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	streamCtx, cancelStream := context.WithCancel(parent)
	events := make(chan tea.Msg, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		followLogEvents(streamCtx, client, opts, events)
	}()

	styles := newTerminalStyles(cmd.OutOrStdout())
	program := tea.NewProgram(
		newFollowLogsModel(events, cancelStream, opts, styles),
		tea.WithContext(parent),
		tea.WithInput(cmd.InOrStdin()),
		tea.WithOutput(cmd.OutOrStdout()),
		tea.WithAltScreen(),
	)
	final, err := program.Run()
	cancelStream()
	<-done
	if model, ok := final.(followLogsModel); ok && model.err != nil {
		return fmt.Errorf("follow request logs: %w", model.err)
	}
	if err == nil || errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	if errors.Is(err, tea.ErrProgramKilled) && parent.Err() != nil {
		return nil
	}
	return fmt.Errorf("run live request viewer: %w", err)
}

func followLogEvents(ctx context.Context, client *agentctl.Client, opts logs.ListOptions, events chan<- tea.Msg) {
	defer close(events)
	for {
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := client.Status(statusCtx)
		cancel()
		if err != nil {
			if !isTransientFollowError(err) {
				sendFollowLogEvent(ctx, events, followLogStateMsg{state: "error", detail: "agent status failed", err: err})
				return
			}
			state, detail := "reconnecting", "agent connection interrupted"
			if agentctl.IsUnavailable(err) {
				state, detail = "waiting", "agent not running"
			}
			if !sendFollowLogEvent(ctx, events, followLogStateMsg{state: state, detail: detail}) || !waitForFollowRetry(ctx) {
				return
			}
			continue
		}
		if !sendFollowLogEvent(ctx, events, followLogStateMsg{state: "connected", bootID: status.BootID}) {
			return
		}

		err = client.FollowLogs(ctx, opts, func(entry logs.Entry) error {
			if !sendFollowLogEvent(ctx, events, followLogEntryMsg{entry: entry}) {
				return ctx.Err()
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if !isTransientFollowError(err) {
			sendFollowLogEvent(ctx, events, followLogStateMsg{state: "error", detail: "live log stream failed", bootID: status.BootID, err: err})
			return
		}
		detail := "log stream interrupted"
		if agentctl.IsUnavailable(err) {
			detail = "agent not running"
		}
		if !sendFollowLogEvent(ctx, events, followLogStateMsg{state: "reconnecting", detail: detail, bootID: status.BootID}) || !waitForFollowRetry(ctx) {
			return
		}
	}
}

func isTransientFollowError(err error) bool {
	return agentctl.IsUnavailable(err) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

func sendFollowLogEvent(ctx context.Context, events chan<- tea.Msg, msg tea.Msg) bool {
	select {
	case events <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForFollowRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func followLogFilterSummary(opts logs.ListOptions) string {
	filters := make([]string, 0, 5)
	if opts.Route != "" {
		filters = append(filters, "route="+terminalEscapeString(opts.Route))
	}
	if opts.Source != "" {
		filters = append(filters, "source="+terminalEscapeString(string(opts.Source)))
	}
	if opts.Method != "" {
		filters = append(filters, "method="+terminalEscapeString(opts.Method))
	}
	if opts.Status != 0 {
		filters = append(filters, fmt.Sprintf("status=%d", opts.Status))
	}
	if !opts.Since.IsZero() {
		filters = append(filters, "since="+opts.Since.Local().Format("15:04:05"))
	}
	if len(filters) == 0 {
		return "all requests"
	}
	return strings.Join(filters, "  ")
}

func formatFollowLogHeader(width int, styles terminalStyles) string {
	return formatFollowLogColumns(logs.Entry{
		Source: "SOURCE", Route: "ROUTE", Method: "METHOD", RequestPath: "PATH", ID: "ID",
	}, width, styles, true)
}

func formatFollowLogEntry(entry logs.Entry, width int, styles terminalStyles) string {
	return formatFollowLogColumns(entry, width, styles, false)
}

func formatFollowLogColumns(entry logs.Entry, width int, styles terminalStyles, header bool) string {
	timeText := entry.StartedAt.Local().Format("15:04:05")
	statusText := fmt.Sprintf("%d", entry.Status)
	durationText := formatLogDuration(entry.Duration)
	if header {
		timeText, statusText, durationText = "TIME", "STS", "DURATION"
	}

	type column struct {
		value string
		width int
		kind  string
	}
	columns := []column{{timeText, 8, "muted"}}
	if width >= 80 {
		columns = append(columns, column{terminalEscapeString(string(entry.Source)), 6, "source"})
	}
	columns = append(columns,
		column{terminalEscapeString(entry.Method), 7, "method"},
		column{statusText, 3, "status"},
	)
	if width >= 110 {
		columns = append(columns, column{durationText, 8, "muted"})
	}
	if width >= 80 {
		routeWidth := 16
		if width >= 120 {
			routeWidth = 20
		}
		columns = append(columns, column{terminalEscapeString(entry.Route), routeWidth, "route"})
	}
	idWidth := 20
	fixedWidth := idWidth + 2*(len(columns)+1)
	for _, col := range columns {
		fixedWidth += col.width
	}
	columns = append(columns,
		column{terminalEscapeString(entry.RequestPath), max(4, width-fixedWidth), "path"},
		column{terminalEscapeString(entry.ID), idWidth, "id"},
	)

	parts := make([]string, 0, len(columns))
	for _, col := range columns {
		value := fitTerminalText(col.value, col.width)
		if header {
			parts = append(parts, styles.label(value))
			continue
		}
		switch col.kind {
		case "status":
			value = fitTerminalText(styles.statusCode(entry.Status), col.width)
		case "source":
			if entry.Source == logs.SourcePublic {
				value = styles.accent(value)
			} else {
				value = styles.muted(value)
			}
		case "method", "route":
			value = styles.label(value)
		case "id", "muted":
			value = styles.muted(value)
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "  ")
}

func fitTerminalText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	plainWidth := lipgloss.Width(value)
	if plainWidth <= width {
		return value + strings.Repeat(" ", width-plainWidth)
	}
	tail := "..."
	if width <= len(tail) {
		tail = ""
	}
	return ansi.Truncate(value, width, tail)
}

func joinAcross(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(right) >= width {
		return clipTerminalLine(right, width)
	}
	left = clipTerminalLine(left, width-lipgloss.Width(right)-1)
	padding := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", padding) + right
}

func clipTerminalLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	tail := "..."
	if width <= len(tail) {
		tail = ""
	}
	return ansi.Truncate(value, width, tail)
}
