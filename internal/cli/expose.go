package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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
		Example: "routeup expose api-myapp --port 8080",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	return cmd
}

func runExpose(cmd *cobra.Command, args []string, cwd string, opts exposeOpts) error {
	if err := certs.EnsureLocalCA(); err != nil {
		return err
	}

	serverURL, token := resolveServerToken(opts.server, opts.token)
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

	routeName := resolveExposeRouteName(positional, discovered.Config)
	if opts.random {
		routeName = route.RandomName()
	}
	if routeName == "" {
		return errors.New("provide a route name or use --random")
	}

	normalizedName := normalizePublicName(routeName)
	exposePaths, err := route.NormalizePathPatterns(discovered.Config.Expose.Paths)
	if err != nil {
		return err
	}

	return startTunnel(cmd, serverURL, token, routeName, normalizedName, opts.port, targetFlags, discovered.Config, exposePaths)
}

// startTunnel ensures the agent is running, sends the expose request, prints
// the route info, and blocks until Ctrl-C.
func startTunnel(cmd *cobra.Command, serverURL, token, localRouteName, publicRouteName string, portFlag int, targetFlags []route.Target, file config.Config, exposePaths []string) error {
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

	targets, port, err := exposeTargets(startCtx, client, localRouteName, portFlag, targetFlags, file)
	if err != nil {
		return err
	}

	host, stopExpose, err := holdExposure(ctx, client, ipc.ExposeRequest{
		Name:     publicRouteName,
		Port:     port,
		Targets:  targets,
		Paths:    exposePaths,
		Server:   serverURL,
		Token:    token,
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		return err
	}
	defer stopExpose()

	out := cmd.OutOrStdout()
	printRouteLocal(out, localRouteName)
	_, _ = fmt.Fprintf(out, "public: https://%s\n", host)
	printTargets(out, targets)
	_, _ = fmt.Fprintf(out, "expose: %s\n", formatExposePaths(exposePaths))
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "press Ctrl-C to stop")

	<-ctx.Done()
	return nil
}

func exposeTargets(ctx context.Context, client *agentctl.Client, routeName string, portFlag int, targetFlags []route.Target, file config.Config) ([]route.Target, int, error) {
	if !hasTargetOverride(portFlag, targetFlags) {
		claims, err := client.List(ctx)
		if err == nil {
			for _, claim := range claims {
				if claim.Name == routeName && len(claim.Targets) > 0 {
					return claim.Targets, route.PrimaryPort(claim.Targets), nil
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
		return nil, 0, fmt.Errorf("resolve expose targets: %w", err)
	}
	return targets, port, nil
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
		_ = client.Unexpose(stopCtx, resp.Host)
	}
	return resp.Host, stop, nil
}

func printRouteLocal(out io.Writer, routeName string) {
	if routeName == "" {
		return
	}
	n, err := route.Parse(routeName)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(out, "route: %s\n", n)
	_, _ = fmt.Fprintf(out, "local: https://%s\n", n.LocalHost())
}

func resolveExposeRouteName(positional string, file config.Config) string {
	if positional != "" {
		return positional
	}
	if env := strings.TrimSpace(os.Getenv("ROUTEUP_NAME")); env != "" {
		return env
	}
	return file.Name
}

func resolveServerToken(flagServer, flagToken string) (server, token string) {
	cc, _ := state.ReadClientConfig()
	server = firstNonEmpty(flagServer, strings.TrimSpace(os.Getenv("ROUTEUP_SERVER")), cc.Server)
	token = firstNonEmpty(flagToken, strings.TrimSpace(os.Getenv("ROUTEUP_TOKEN")), cc.Token)
	return server, token
}

func normalizePublicName(label string) (normalizedName string) {
	if label == "" || !strings.Contains(label, ".") {
		return label
	}
	normalized := strings.ReplaceAll(label, ".", "-")
	return normalized
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
