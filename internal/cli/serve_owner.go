package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/state"
)

func runServeOwner(ctx context.Context, version string, plan servePlan, followLogs bool, out, errOut io.Writer, ready func(routeReadyEvent) error) error {
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(sockPath, "", version)
	ownerCtx, cancelOwner := context.WithCancel(ctx)
	defer cancelOwner()
	ownerLease, err := state.RegisterOwner(plan.Route, state.OwnerServe, os.Getpid())
	if err != nil {
		return fmt.Errorf("record serve owner: %w", err)
	}
	defer func() { _ = ownerLease.Release() }()

	startCtx, cancelStart := context.WithTimeout(ownerCtx, 12*time.Second)
	ensured, err := client.EnsureRunning(startCtx)
	cancelStart()
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	if ensured == agentctl.EnsureRestarted {
		_, _ = fmt.Fprintln(errOut, "note: restarted the local agent to pick up a new build")
	}

	claim := ipc.Claim{
		Name:            plan.Route,
		Port:            route.PrimaryPort(plan.Targets),
		Targets:         plan.Targets,
		CaptureRequest:  plan.CaptureRequest,
		CaptureResponse: plan.CaptureResponse,
		RedactHeaders:   plan.RedactHeaders,
		OwnerPID:        os.Getpid(),
		OwnerCWD:        plan.CWD,
	}
	registerCtx, cancelRegister := context.WithTimeout(ownerCtx, 5*time.Second)
	registered, err := client.Register(registerCtx, claim)
	cancelRegister()
	if err != nil {
		if _, ok := errors.AsType[*ipc.ConflictError](err); ok {
			return fmt.Errorf("%w\n  hint: run `routeup stop %s`; non-serve owners must be stopped from their terminal", err, plan.Route)
		}
		return err
	}
	claim.RegisteredAt = registered.RegisteredAt
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Unregister(shutdownCtx, claim.Name, claim.OwnerPID)
	}()

	publicHost := ""
	var exposeReq *ipc.ExposeRequest
	if plan.Exposure != nil {
		request := *plan.Exposure
		request.OwnerPID = claim.OwnerPID
		host, stopExpose, exposeErr := holdExposure(ownerCtx, client, request)
		if exposeErr != nil {
			return exposeErr
		}
		defer stopExpose()
		publicHost = host
		exposeReq = &request
	}

	maintainDone := make(chan struct{})
	go func() {
		defer close(maintainDone)
		client.Maintain(ownerCtx, agentctl.DesiredState{
			Claim: &claim, Exposure: exposeReq, PublicHost: publicHost,
		}, errOut)
	}()

	controlReady := make(chan struct{}, 1)
	controlDone := make(chan bool, 1)
	go func() {
		controlDone <- maintainServeControl(ownerCtx, client, claim.Name, claim.OwnerPID, controlReady)
	}()

	controlTimer := time.NewTimer(12 * time.Second)
	defer controlTimer.Stop()
	select {
	case <-controlReady:
	case stopped := <-controlDone:
		cancelOwner()
		<-maintainDone
		if stopped {
			return errors.New("route stopped before becoming ready")
		}
		return ownerCtx.Err()
	case <-controlTimer.C:
		cancelOwner()
		<-maintainDone
		<-controlDone
		return errors.New("route owner control did not become ready")
	case <-ownerCtx.Done():
		<-maintainDone
		<-controlDone
		return nil
	}
	routeName, err := route.Parse(plan.Route)
	if err != nil {
		cancelOwner()
		<-maintainDone
		<-controlDone
		return err
	}
	publicURL := ""
	if publicHost != "" {
		publicURL = "https://" + publicHost
	}
	event := routeReadyEvent{
		Route: plan.Route, LocalURL: localURL(routeName.LocalHost(), state.TLSPortOrDefault()),
		PublicURL: publicURL, ExposurePaths: plan.ExposurePaths, Targets: plan.Targets,
		OwnerPID: claim.OwnerPID,
	}
	if err := ready(event); err != nil {
		cancelOwner()
		<-maintainDone
		<-controlDone
		return err
	}

	var logsDone <-chan error
	if followLogs {
		ch := make(chan error, 1)
		logsDone = ch
		go func() {
			ch <- followServeLogs(ownerCtx, client, logs.ListOptions{
				Route: claim.Name, Since: registered.RegisteredAt,
			}, out, errOut)
		}()
	}

	var result error
	controlFinished := false
	logsFinished := false
	select {
	case <-ownerCtx.Done():
	case <-controlDone:
		controlFinished = true
	case err := <-logsDone:
		result = err
		logsFinished = true
	}
	cancelOwner()
	<-maintainDone
	if !controlFinished {
		<-controlDone
	}
	if logsDone != nil && !logsFinished {
		select {
		case err := <-logsDone:
			if result == nil {
				result = err
			}
		default:
		}
	}
	return result
}

func maintainServeControl(ctx context.Context, client *agentctl.Client, name string, ownerPID int, ready chan<- struct{}) bool {
	readySent := false
	for {
		stopped, err := client.WatchRouteOwner(ctx, name, ownerPID, func() {
			if readySent {
				return
			}
			readySent = true
			select {
			case ready <- struct{}{}:
			default:
			}
		})
		if stopped {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		_ = err
		if !waitForFollowRetry(ctx) {
			return false
		}
	}
}

type serveLogWriteError struct {
	err error
}

func (e *serveLogWriteError) Error() string { return e.err.Error() }
func (e *serveLogWriteError) Unwrap() error { return e.err }

func followServeLogs(ctx context.Context, client *agentctl.Client, opts logs.ListOptions, out, errOut io.Writer) error {
	lastID := ""
	wroteHeader := false
	for {
		err := client.FollowLogsFrom(ctx, opts, lastID, func(entry logs.Entry) error {
			if !wroteHeader {
				if err := writeLogHeader(out); err != nil {
					return &serveLogWriteError{err: err}
				}
				wroteHeader = true
			}
			if err := writeLogEntry(out, entry, false); err != nil {
				return &serveLogWriteError{err: err}
			}
			lastID = entry.ID
			return nil
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !isTransientFollowError(err) {
			var writeErr *serveLogWriteError
			if errors.As(err, &writeErr) {
				return writeErr.err
			}
			_, _ = fmt.Fprintf(errOut, "routeup: live request logs interrupted: %v; retrying\n", err)
		}
		if !waitForFollowRetry(ctx) {
			return nil
		}
	}
}

func writeServeReady(cmd *cobra.Command, event routeReadyEvent, opts serveOpts) error {
	out := cmd.OutOrStdout()
	if opts.json {
		return writeRouteReadyEvent(out, event)
	}
	styles := newTerminalStyles(out)
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("route:"), styles.accent(event.Route))
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("local:"), styles.url(event.LocalURL))
	if event.PublicURL != "" {
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("public:"), styles.url(event.PublicURL))
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("expose:"), formatExposePaths(event.ExposurePaths))
	}
	printTargets(out, event.Targets)
	if opts.qr {
		qrURL := event.LocalURL
		if event.PublicURL != "" {
			qrURL = event.PublicURL
		}
		writeRouteQR(out, qrURL)
	}
	_, _ = fmt.Fprintln(out)
	if event.Detached {
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("status:"), styles.success(fmt.Sprintf("running in background (pid %d)", event.OwnerPID)))
		_, _ = fmt.Fprintf(out, "%s routeup stop %s\n", styles.label("stop:"), event.Route)
		_, _ = fmt.Fprintf(out, "%s routeup logs %s --follow\n", styles.label("logs:"), event.Route)
		return nil
	}
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("requests:"), styles.muted("live"))
	_, _ = fmt.Fprintln(out, styles.muted("press Ctrl-C to stop"))
	return nil
}
