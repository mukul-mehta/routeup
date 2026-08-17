package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

type exposeOpts struct {
	port    int
	targets []string
	random  bool
	server  string
	token   string
	json    bool
	qr      bool
}

func newExposeCmd() *cobra.Command {
	var opts exposeOpts

	cmd := &cobra.Command{
		Use:   "expose [name]",
		Short: "Expose a local route publicly through a routeup server",
		Long: "Expose a local app on a public URL via a routeup server.\n\n" +
			"The public host is decided by the server from your token (or its\n" +
			"public namespace when you have no token), so you pass a route name and\n" +
			"the server returns the full URL. The tunnel is held until you stop it",
		Example: "routeup expose api.example-app --port 8080",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.random && len(args) != 0 {
				return errors.New("a route name and --random cannot be used together")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runExpose(cmd, args, cwd, opts)
		},
	}

	cmd.Flags().IntVar(&opts.port, "port", 0, "local app port")
	cmd.Flags().StringArrayVar(&opts.targets, "target", nil, "path target in /path=port form (repeatable)")
	cmd.Flags().BoolVar(&opts.random, "random", false, "use a random route name")
	cmd.Flags().StringVar(&opts.server, "server", "", "public server URL (or ROUTEUP_SERVER)")
	cmd.Flags().StringVar(&opts.token, "token", "", "server token (or ROUTEUP_TOKEN)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "write the ready event as JSON")
	cmd.Flags().BoolVar(&opts.qr, "qr", false, "print a QR code for the public URL")
	cmd.MarkFlagsMutuallyExclusive("json", "qr")
	return cmd
}

func runExpose(cmd *cobra.Command, args []string, cwd string, opts exposeOpts) error {
	if err := certs.EnsureLocalCA(); err != nil {
		return err
	}

	serverURL, token, err := resolveServerToken(opts.server, opts.token)
	if err != nil {
		return err
	}
	if serverURL == "" {
		return errors.New("no server set — pass --server, set ROUTEUP_SERVER, or run `routeup setup --server …`")
	}

	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	discovered, err := config.Discover(cwd)
	if err != nil {
		return err
	}
	targetFlags, err := parseTargetFlags(opts.targets)
	if err != nil {
		return err
	}

	resolvedRoute, err := resolveExposeRoute(positional, discovered.Config, opts.random, cwd)
	if err != nil {
		return err
	}

	routeName := resolvedRoute.String()
	normalizedName := normalizePublicName(resolvedRoute)
	exposePaths, err := route.NormalizePathPatterns(discovered.Config.Expose.Paths)
	if err != nil {
		return err
	}

	return startTunnel(cmd, serverURL, token, routeName, normalizedName, opts.port, targetFlags, discovered.Config, exposePaths, opts)
}

// startTunnel ensures the agent is running, sends the expose request, prints
// the route info, and blocks until Ctrl-C.
func startTunnel(cmd *cobra.Command, serverURL, token, localRouteName, publicRouteName string, portFlag int, targetFlags []route.Target, file config.Config, exposePaths []string, commandOpts exposeOpts) error {
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

	startCtx, cancelStart := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStart()
	if _, err := client.EnsureRunning(startCtx); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	targets, port, hasLocalRoute, captureRequest, captureResponse, redactHeaders, err := exposeTargets(startCtx, client, localRouteName, portFlag, targetFlags, file)
	if err != nil {
		return err
	}

	exposeReq := ipc.ExposeRequest{
		Name:            publicRouteName,
		Route:           localRouteName,
		Port:            port,
		Targets:         targets,
		Paths:           exposePaths,
		CaptureRequest:  captureRequest,
		CaptureResponse: captureResponse,
		RedactHeaders:   redactHeaders,
		Server:          serverURL,
		Token:           token,
		OwnerPID:        os.Getpid(),
	}
	host, stopExpose, err := holdExposure(ctx, client, exposeReq)
	if err != nil {
		return err
	}
	defer stopExpose()

	out := cmd.OutOrStdout()
	publicURL := "https://" + host
	localRouteURL := ""
	if hasLocalRoute {
		if n, parseErr := route.Parse(localRouteName); parseErr == nil {
			localRouteURL = localURL(n.LocalHost(), state.TLSPortOrDefault())
		}
	}
	if commandOpts.json {
		if err := writeRouteReadyEvent(out, routeReadyEvent{
			Route: localRouteName, LocalURL: localRouteURL, PublicURL: publicURL,
			ExposurePaths: exposePaths, Targets: targets,
		}); err != nil {
			return err
		}
	} else {
		styles := newTerminalStyles(out)
		if hasLocalRoute {
			printRouteLocal(out, localRouteName, state.TLSPortOrDefault())
		}
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("public:"), styles.url(publicURL))
		printTargets(out, targets)
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("expose:"), formatExposePaths(exposePaths))
		if commandOpts.qr {
			writeRouteQR(out, publicURL)
		}
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, styles.muted("press Ctrl-C to stop"))
	}

	client.Maintain(ctx, agentctl.DesiredState{
		Exposure: &exposeReq, PublicHost: host,
	}, cmd.ErrOrStderr())
	return nil
}

func exposeTargets(ctx context.Context, client *agentctl.Client, routeName string, portFlag int, targetFlags []route.Target, file config.Config) ([]route.Target, int, bool, bool, bool, []string, error) {
	if !hasTargetOverride(portFlag, targetFlags) {
		claims, err := client.List(ctx)
		if err != nil && !agentctl.IsUnavailable(err) {
			return nil, 0, false, false, false, nil, fmt.Errorf("list active routes: %w", err)
		}
		if err == nil {
			for _, claim := range claims {
				if claim.Name == routeName && len(claim.Targets) > 0 {
					return claim.Targets, route.PrimaryPort(claim.Targets), true, claim.CaptureRequest, claim.CaptureResponse, claim.RedactHeaders, nil
				}
			}
		}
	}

	targets, port, err := config.ResolveTargets(config.Inputs{
		PortFlag:    portFlag,
		TargetFlags: targetFlags,
		Env:         os.Getenv,
		File:        file,
	})
	if err != nil {
		return nil, 0, false, false, false, nil, fmt.Errorf("resolve expose targets: %w", err)
	}
	return targets, port, false, file.Capture.Request, file.Capture.Response, file.Capture.RedactHeaders, nil
}

func hasTargetOverride(portFlag int, targetFlags []route.Target) bool {
	return portFlag != 0 || len(targetFlags) != 0 || strings.TrimSpace(os.Getenv("ROUTEUP_PORT")) != ""
}

// holdExposure sends the expose request to the agent and returns the granted
// host plus a stop func. Non-blocking — the caller owns the lifetime.
// Shared by `expose` and `serve --expose`.
func holdExposure(ctx context.Context, client *agentctl.Client, req ipc.ExposeRequest) (string, func(), error) {
	exposeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.Expose(exposeCtx, req)
	if err != nil {
		return "", nil, fmt.Errorf("expose: %w", err)
	}
	stop := func() {
		stopCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = client.Unexpose(stopCtx, ipc.UnexposeRequest{
			Host: resp.Host, Route: req.Route, OwnerPID: req.OwnerPID,
		})
	}
	return resp.Host, stop, nil
}

func printRouteLocal(out io.Writer, routeName string, tlsPort int) {
	if routeName == "" {
		return
	}
	n, err := route.Parse(routeName)
	if err != nil {
		return
	}
	styles := newTerminalStyles(out)
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("route:"), styles.accent(n.String()))
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.label("local:"), styles.url(localURL(n.LocalHost(), tlsPort)))
}

func resolveExposeRoute(positional string, file config.Config, random bool, cwd string) (route.Name, error) {
	if random {
		return route.Parse(route.RandomName())
	}
	return config.ResolveName(config.Inputs{
		PositionalName: positional,
		Env:            os.Getenv,
		File:           file,
		DirName:        filepath.Base(cwd),
	})
}

func resolveServerToken(flagServer, flagToken string) (server, token string, err error) {
	envServer := strings.TrimSpace(os.Getenv("ROUTEUP_SERVER"))
	envToken := strings.TrimSpace(os.Getenv("ROUTEUP_TOKEN"))
	server = firstNonEmpty(flagServer, envServer)
	token = firstNonEmpty(flagToken, envToken)

	var cc state.ClientConfig
	if server == "" || token == "" {
		cc, err = state.ReadClientConfig()
		if err != nil {
			return "", "", err
		}
	}
	if server == "" {
		server = cc.Server
	}
	server, err = normalizeServerURL(server)
	if err != nil {
		return "", "", err
	}
	if token == "" && sameServer(server, cc.Server) {
		token = cc.Token
	}
	return server, token, nil
}

func normalizeServerURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid public server URL %q", value)
	}
	if parsed.User != nil {
		return "", errors.New("public server URL cannot contain user information")
	}
	if parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("public server URL cannot contain a path, query, or fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" {
		return "", fmt.Errorf("invalid public server URL %q", value)
	}
	if parsed.Scheme != "https" {
		loopback := hostname == "localhost"
		if address, parseErr := netip.ParseAddr(hostname); parseErr == nil {
			loopback = address.IsLoopback()
		}
		if parsed.Scheme != "http" || !loopback {
			return "", errors.New("public server URL must use HTTPS (HTTP is allowed only for loopback testing)")
		}
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func sameServer(a, b string) bool {
	normalizedA, errA := normalizeServerURL(a)
	normalizedB, errB := normalizeServerURL(b)
	return errA == nil && errB == nil && normalizedA != "" && normalizedA == normalizedB
}

func normalizePublicName(name route.Name) string {
	return strings.ReplaceAll(name.String(), ".", "-")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
