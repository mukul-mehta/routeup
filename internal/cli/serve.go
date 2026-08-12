package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/state"
)

type serveOpts struct {
	port    int
	targets []string
	expose  bool
	random  bool
	server  string
	token   string
	json    bool
	qr      bool
}

func newServeCmd() *cobra.Command {
	var opts serveOpts

	cmd := &cobra.Command{
		Use:   "serve [name]",
		Short: "Serve a local app on a stable HTTPS route",
		Long: "Serve a local app on https://<name>.localhost.\n\n" +
			"The route name comes from the argument, or from routeup.json or the\n" +
			"package.json \"routeup\" block when omitted. A bare name is prefixed\n" +
			"with the project name; a dotted name is taken literally:\n\n" +
			"  serve myapp      ->  https://myapp.localhost\n" +
			"  serve api        ->  https://api.<project>.localhost\n" +
			"  serve api.myapp  ->  https://api.myapp.localhost\n\n" +
			"Add --expose, or set expose.enabled in config, to also publish it through\n" +
			"a routeup server (the same as `routeup expose`); the public name is a\n" +
			"single label under your token's namespace.",
		Example: "  routeup serve myapp --port 3000\n" +
			"  routeup serve api.myapp --port 8080\n" +
			"  routeup serve myapp --port 3000 --expose",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.random && len(args) != 0 {
				return errors.New("a route name and --random cannot be used together")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runServe(cmd, args, cwd, opts)
		},
	}

	cmd.Flags().IntVar(&opts.port, "port", 0, "port your local app listens on")
	cmd.Flags().StringArrayVar(&opts.targets, "target", nil, "path target in /path=port form (repeatable)")
	cmd.Flags().BoolVar(&opts.expose, "expose", false, "also expose this route publicly through a routeup server")
	cmd.Flags().BoolVar(&opts.expose, "public", false, "alias for --expose")
	cmd.Flags().BoolVar(&opts.random, "random", false, "use a random route name")
	cmd.Flags().StringVar(&opts.server, "server", "", "with --expose, public server URL (or ROUTEUP_SERVER, or saved by setup)")
	cmd.Flags().StringVar(&opts.token, "token", "", "with --expose, server token (or ROUTEUP_TOKEN, or saved by setup)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "write the ready event as JSON")
	cmd.Flags().BoolVar(&opts.qr, "qr", false, "print a QR code for the route URL")
	cmd.MarkFlagsMutuallyExclusive("json", "qr")

	return cmd
}

func runServe(cmd *cobra.Command, args []string, cwd string, opts serveOpts) error {
	if err := certs.EnsureLocalCA(); err != nil {
		return err
	}

	discovered, err := config.Discover(cwd)
	if err != nil {
		return err
	}

	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	if opts.random {
		positional = route.RandomName()
	}

	targetFlags, err := parseTargetFlags(opts.targets)
	if err != nil {
		return err
	}

	resolved, err := config.Resolve(config.Inputs{
		PositionalName: positional,
		PortFlag:       opts.port,
		TargetFlags:    targetFlags,
		Env:            os.Getenv,
		File:           discovered.Config,
		DirName:        filepath.Base(cwd),
	})
	if err != nil {
		return err
	}

	tlsPort := state.TLSPortOrDefault()
	out := cmd.OutOrStdout()

	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	startCtx, cancelStart := context.WithTimeout(ctx, 12*time.Second)
	defer cancelStart()
	ensured, err := client.EnsureRunning(startCtx)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	if ensured == agentctl.EnsureRestarted {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "note: restarted the local agent to pick up a new build")
	}

	claim := ipc.Claim{
		Name:            resolved.Route.String(),
		Port:            resolved.Port,
		Targets:         resolved.Targets,
		CaptureRequest:  discovered.Config.Capture.Request,
		CaptureResponse: discovered.Config.Capture.Response,
		RedactHeaders:   discovered.Config.Capture.RedactHeaders,
		OwnerPID:        os.Getpid(),
		OwnerCWD:        cwd,
	}

	if _, err := client.Register(startCtx, claim); err != nil {
		if _, ok := errors.AsType[*ipc.ConflictError](err); ok {
			return fmt.Errorf("%w\n  hint: stop the holding process or pick a different route name", err)
		}
		return err
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Unregister(shutdownCtx, claim.Name, claim.OwnerPID)
	}()

	exposePaths, err := route.NormalizePathPatterns(discovered.Config.Expose.Paths)
	if err != nil {
		return err
	}

	var publicHost string
	var exposeReq *ipc.ExposeRequest
	if opts.expose || discovered.Config.Expose.Enabled {
		host, request, stopExpose, err := serveExpose(ctx, client, resolved.Route, resolved.Targets, exposePaths, discovered.Config.Capture.Request, discovered.Config.Capture.Response, discovered.Config.Capture.RedactHeaders, opts)
		if err != nil {
			return err
		}
		defer stopExpose()
		publicHost = host
		exposeReq = &request
	}

	localRouteURL := localURL(resolved.Route.LocalHost(), tlsPort)
	publicURL := ""
	if publicHost != "" {
		publicURL = "https://" + publicHost
	}
	if opts.json {
		if err := writeRouteReadyEvent(out, routeReadyEvent{
			Route: resolved.Route.String(), LocalURL: localRouteURL, PublicURL: publicURL,
			ExposurePaths: exposePaths, Targets: resolved.Targets,
		}); err != nil {
			return err
		}
	} else {
		styles := newTerminalStyles(out)
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("route:"), styles.accent(resolved.Route.String()))
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("local:"), styles.url(localRouteURL))
		if publicHost != "" {
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("public:"), styles.url(publicURL))
			_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("expose:"), formatExposePaths(exposePaths))
		}
		printTargets(out, resolved.Targets)
		if opts.qr {
			qrURL := localRouteURL
			if publicURL != "" {
				qrURL = publicURL
			}
			writeRouteQR(out, qrURL)
		}
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, styles.muted("press Ctrl-C to stop"))
	}

	client.Maintain(ctx, agentctl.DesiredState{
		Claim: &claim, Exposure: exposeReq, PublicHost: publicHost,
	}, cmd.ErrOrStderr())
	return nil
}

func serveExpose(ctx context.Context, client *agentctl.Client, routeName route.Name, targets []route.Target, paths []string, captureRequest bool, captureResponse bool, redactHeaders []string, opts serveOpts) (string, ipc.ExposeRequest, func(), error) {
	serverURL, token, err := resolveServerToken(opts.server, opts.token)
	if err != nil {
		return "", ipc.ExposeRequest{}, nil, err
	}
	if serverURL == "" {
		return "", ipc.ExposeRequest{}, nil, errors.New("public exposure needs a server — pass --server, set ROUTEUP_SERVER, or run `routeup setup --server …`")
	}

	req := ipc.ExposeRequest{
		Name:            normalizePublicName(routeName),
		Route:           routeName.String(),
		Port:            route.PrimaryPort(targets),
		Targets:         targets,
		Paths:           paths,
		CaptureRequest:  captureRequest,
		CaptureResponse: captureResponse,
		RedactHeaders:   redactHeaders,
		Server:          serverURL,
		Token:           token,
		OwnerPID:        os.Getpid(),
	}
	host, stop, err := holdExposure(ctx, client, req)
	return host, req, stop, err
}

func localURL(host string, port int) string {
	if port == 443 {
		return fmt.Sprintf("https://%s", host)
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}
