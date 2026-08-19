package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
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
	detach  bool
}

type servePlan struct {
	Route           string             `json:"route"`
	Targets         []route.Target     `json:"targets"`
	CaptureRequest  bool               `json:"capture_request,omitempty"`
	CaptureResponse bool               `json:"capture_response,omitempty"`
	RedactHeaders   []string           `json:"redact_headers,omitempty"`
	ExposurePaths   []string           `json:"exposure_paths,omitempty"`
	Exposure        *ipc.ExposeRequest `json:"exposure,omitempty"`
	CWD             string             `json:"cwd"`
}

func newServeCmd() *cobra.Command {
	var opts serveOpts

	cmd := &cobra.Command{
		Use:   "serve [name]",
		Short: "Serve a local app on a stable HTTPS route",
		Long: "Serve a local app on https://<name>.localhost.\n\n" +
			"The route name comes from the argument, or from routeup.json or the\n" +
			"package.json \"routeup\" block when omitted. An explicit name is\n" +
			"always taken literally:\n\n" +
			"  serve example-app      ->  https://example-app.localhost\n" +
			"  serve api              ->  https://api.localhost\n" +
			"  serve api.example-app  ->  https://api.example-app.localhost\n\n" +
			"Add --expose, or set expose.enabled in config, to also publish it through\n" +
			"a routeup server (the same as `routeup expose`); the public name is a\n" +
			"single label under your token's namespace.",
		Example: "  routeup serve example-app --port 3000\n" +
			"  routeup serve api.example-app --port 8080\n" +
			"  routeup serve example-app --port 3000 --expose",
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
	cmd.Flags().BoolVarP(&opts.detach, "detach", "d", false, "keep the route running in the background")
	cmd.MarkFlagsMutuallyExclusive("json", "qr")

	return cmd
}

func runServe(cmd *cobra.Command, args []string, cwd string, opts serveOpts) error {
	if err := certs.EnsureLocalCA(); err != nil {
		return err
	}
	plan, err := resolveServePlan(args, cwd, opts)
	if err != nil {
		return err
	}
	if opts.detach {
		event, err := startDetachedServe(cmd.Context(), cmd.Root().Version, plan)
		if err != nil {
			return err
		}
		event.Detached = true
		return writeServeReady(cmd, event, opts)
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServeOwner(ctx, cmd.Root().Version, plan, !opts.json, cmd.OutOrStdout(), cmd.ErrOrStderr(), func(event routeReadyEvent) error {
		return writeServeReady(cmd, event, opts)
	})
}

func resolveServePlan(args []string, cwd string, opts serveOpts) (servePlan, error) {
	discovered, err := config.Discover(cwd)
	if err != nil {
		return servePlan{}, err
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
		return servePlan{}, err
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
		return servePlan{}, err
	}

	exposePaths, err := route.NormalizePathPatterns(discovered.Config.Expose.Paths)
	if err != nil {
		return servePlan{}, err
	}
	plan := servePlan{
		Route:           resolved.Route.String(),
		Targets:         resolved.Targets,
		CaptureRequest:  discovered.Config.Capture.Request,
		CaptureResponse: discovered.Config.Capture.Response,
		RedactHeaders:   discovered.Config.Capture.RedactHeaders,
		ExposurePaths:   exposePaths,
		CWD:             cwd,
	}
	if opts.expose || discovered.Config.Expose.Enabled {
		serverURL, token, err := resolveServerToken(opts.server, opts.token)
		if err != nil {
			return servePlan{}, err
		}
		if serverURL == "" {
			return servePlan{}, errors.New("public exposure needs a server — pass --server, set ROUTEUP_SERVER, or run `routeup setup --server …`")
		}
		plan.Exposure = &ipc.ExposeRequest{
			Name:            normalizePublicName(resolved.Route),
			Route:           resolved.Route.String(),
			Port:            resolved.Port,
			Targets:         resolved.Targets,
			Paths:           exposePaths,
			CaptureRequest:  discovered.Config.Capture.Request,
			CaptureResponse: discovered.Config.Capture.Response,
			RedactHeaders:   discovered.Config.Capture.RedactHeaders,
			Server:          serverURL,
			Token:           token,
		}
	}
	return plan, nil
}

func localURL(host string, port int) string {
	if port == 443 {
		return fmt.Sprintf("https://%s", host)
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}
