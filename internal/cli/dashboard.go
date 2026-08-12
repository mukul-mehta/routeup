package cli

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/state"
)

type dashboardSnapshotMsg struct {
	status ipc.Status
	routes []ipc.Claim
	online bool
	err    error
}

type dashboardSnapshotClosedMsg struct{}

type dashboardInspectMsg struct {
	id    string
	entry logs.Entry
	err   error
}

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Open the interactive local route dashboard",
		Long: "Open a read-only terminal dashboard for the local routeup agent.\n\n" +
			"The dashboard shows active routes, public exposures, live requests, and\n" +
			"opted-in request captures. It never starts or changes the agent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !terminalIsInteractive(cmd.InOrStdin(), cmd.OutOrStdout()) {
				return errors.New("dashboard requires an interactive terminal; use `routeup routes` or `routeup logs`")
			}
			return runDashboard(cmd)
		},
	}
}

func runDashboard(cmd *cobra.Command) error {
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(socketPath, "", cmd.Root().Version)
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	workerCtx, cancelWorkers := context.WithCancel(parent)
	logEvents := make(chan tea.Msg, 64)
	snapshotEvents := make(chan tea.Msg, 4)

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		followLogEvents(workerCtx, client, dashboardLogOptions(), logEvents)
	}()
	go func() {
		defer workers.Done()
		pollDashboard(workerCtx, client, snapshotEvents)
	}()

	model := newDashboardModel(
		client,
		workerCtx,
		logEvents,
		snapshotEvents,
		cancelWorkers,
		newTerminalStyles(cmd.OutOrStdout()),
		state.TLSPortOrDefault(),
	)
	program := tea.NewProgram(
		model,
		tea.WithContext(parent),
		tea.WithInput(cmd.InOrStdin()),
		tea.WithOutput(cmd.OutOrStdout()),
		tea.WithAltScreen(),
	)
	final, runErr := program.Run()
	cancelWorkers()
	workers.Wait()
	if dashboard, ok := final.(dashboardModel); ok && dashboard.logs.err != nil {
		return fmt.Errorf("dashboard live logs: %w", dashboard.logs.err)
	}
	if runErr == nil || errors.Is(runErr, tea.ErrInterrupted) {
		return nil
	}
	if errors.Is(runErr, tea.ErrProgramKilled) && parent.Err() != nil {
		return nil
	}
	return fmt.Errorf("run dashboard: %w", runErr)
}

func dashboardLogOptions() logs.ListOptions {
	return logs.ListOptions{Limit: followLogLimit}
}

func pollDashboard(ctx context.Context, client *agentctl.Client, events chan<- tea.Msg) {
	defer close(events)
	for {
		msg := fetchDashboardSnapshot(ctx, client)
		if !sendDashboardEvent(ctx, events, msg) || !waitDashboardPoll(ctx) {
			return
		}
	}
}

func fetchDashboardSnapshot(ctx context.Context, client *agentctl.Client) dashboardSnapshotMsg {
	statusCtx, cancelStatus := context.WithTimeout(ctx, 2*time.Second)
	status, err := client.Status(statusCtx)
	cancelStatus()
	if err != nil {
		if agentctl.IsUnavailable(err) || isTransientFollowError(err) {
			return dashboardSnapshotMsg{}
		}
		return dashboardSnapshotMsg{err: fmt.Errorf("get agent status: %w", err)}
	}

	routesCtx, cancelRoutes := context.WithTimeout(ctx, 2*time.Second)
	routes, err := client.List(routesCtx)
	cancelRoutes()
	if err != nil {
		if agentctl.IsUnavailable(err) || isTransientFollowError(err) {
			return dashboardSnapshotMsg{}
		}
		return dashboardSnapshotMsg{status: status, online: true, err: fmt.Errorf("list active routes: %w", err)}
	}
	return dashboardSnapshotMsg{status: status, routes: routes, online: true}
}

func sendDashboardEvent(ctx context.Context, events chan<- tea.Msg, msg tea.Msg) bool {
	select {
	case events <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitDashboardPoll(ctx context.Context) bool {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForDashboardSnapshot(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return dashboardSnapshotClosedMsg{}
		}
		return msg
	}
}

func inspectDashboardRequest(ctx context.Context, client *agentctl.Client, id string) tea.Cmd {
	return func() tea.Msg {
		inspectCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		entry, err := client.Inspect(inspectCtx, id)
		return dashboardInspectMsg{id: id, entry: entry, err: err}
	}
}
