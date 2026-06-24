package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
	"github.com/mukul-mehta/routeup/internal/state"
)

type exposeOpts struct {
	port     int
	random   bool
	insecure bool
	server   string
	token    string
}

// newExposeCmd builds `routeup expose`: it opens a public tunnel through the
// local agent and holds it until Ctrl-C.
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
			return runExpose(cmd, args, cwd, os.Getenv, opts)
		},
	}

	cmd.Flags().IntVar(&opts.port, "port", 0, "local app port")
	cmd.Flags().BoolVar(&opts.random, "random", false, "request a server-assigned random name")
	cmd.Flags().BoolVar(&opts.insecure, "insecure", false, "skip TLS verification (dev only, e.g. a server in internal TLS mode)")
	cmd.Flags().StringVar(&opts.server, "server", "", "public server URL (or ROUTEUP_SERVER)")
	cmd.Flags().StringVar(&opts.token, "token", "", "server token (or ROUTEUP_TOKEN)")
	return cmd
}

func runExpose(cmd *cobra.Command, args []string, cwd string, getenv func(string) string, opts exposeOpts) error {
	serverURL, token := resolveServerToken(opts.server, opts.token, getenv)
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
	routeName := exposeRouteName(positional, getenv, discovered.Config)
	port := resolveExposePort(opts.port, getenv, discovered.Config)

	if !opts.random {
		if err := checkPublicLabel(routeName); err != nil {
			return err
		}
	}

	return exposeReal(cmd, serverURL, token, routeName, port, opts.random, opts.insecure)
}

// exposeReal opens a public tunnel through the local agent and blocks until
// Ctrl-C, releasing the claim on exit.
func exposeReal(cmd *cobra.Command, serverURL, token, routeName string, port int, random, insecure bool) error {
	if port == 0 {
		return errors.New("set --port to the local app's port")
	}
	if routeName == "" && !random {
		return errors.New("provide a route name or use --random")
	}
	// Public exposure runs through the local agent, which needs the local CA.
	if err := preflightCA(); err != nil {
		return err
	}

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
	if _, err := client.EnsureRunning(startCtx); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	host, stopExpose, err := holdExposure(ctx, client, ipc.ExposeRequest{
		Name:     routeName,
		Port:     port,
		Server:   serverURL,
		Token:    token,
		Random:   random,
		Insecure: insecure,
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		return err
	}
	defer stopExpose()

	out := cmd.OutOrStdout()
	printRouteLocal(out, routeName)
	_, _ = fmt.Fprintf(out, "public: https://%s\n", host)
	_, _ = fmt.Fprintf(out, "target: http://localhost:%d\n", port)
	_, _ = fmt.Fprintln(out, "expose: all paths")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "press Ctrl-C to stop")

	<-ctx.Done()
	return nil
}

// holdExposure starts a public tunnel through the agent and returns the granted
// host plus a stop func. It does not block — the caller owns the lifetime and
// calls stop on exit. Shared by `expose` and `serve --expose`.
func holdExposure(ctx context.Context, client *agentctl.Client, req ipc.ExposeRequest) (string, func(), error) {
	exposeCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	resp, err := client.Expose(exposeCtx, req)
	if err != nil {
		return "", nil, fmt.Errorf("expose: %w", err)
	}
	stop := func() {
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = client.Unexpose(sctx, resp.Host)
	}
	return resp.Host, stop, nil
}

// printRouteLocal prints the route and local URL lines when a route name is set.
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

// checkPublicLabel rejects a multi-label public route name early, with a clear
// suggestion, rather than letting the server reject it after the agent connects.
func checkPublicLabel(label string) error {
	if label == "" || !strings.Contains(label, ".") {
		return nil
	}
	suggestion := strings.ReplaceAll(label, ".", "-")
	return fmt.Errorf(
		"public routes are a single label, so %q can't be a public host.\n"+
			"  try:   routeup expose %s   ->  https://%s.<namespace>.routeup.dev\n"+
			"  note:  multi-label names still work locally (https://%s.localhost)",
		label, suggestion, suggestion, label)
}

// exposeRouteName resolves the public route label: the positional name, else
// ROUTEUP_NAME, else the config name. Unlike `serve`, it does NOT prefix the
// project name — publicly the namespace comes from the token, so the label
// stands alone.
func exposeRouteName(positional string, getenv func(string) string, file config.Config) string {
	if positional != "" {
		return positional
	}
	if env := strings.TrimSpace(getenv("ROUTEUP_NAME")); env != "" {
		return env
	}
	return file.Name
}

func resolveExposePort(flagPort int, getenv func(string) string, file config.Config) int {
	if flagPort != 0 {
		return flagPort
	}
	if raw := strings.TrimSpace(getenv("ROUTEUP_PORT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return file.Port
}

// resolveServerToken resolves the public server URL and token, in precedence:
// flag > ROUTEUP_SERVER/ROUTEUP_TOKEN env > the saved client config
// (~/.routeup/client.json from `routeup setup --server/--token`).
func resolveServerToken(flagServer, flagToken string, getenv func(string) string) (server, token string) {
	cc, _ := state.ReadClientConfig()
	server = firstNonEmpty(flagServer, strings.TrimSpace(getenv("ROUTEUP_SERVER")), cc.Server)
	token = firstNonEmpty(flagToken, strings.TrimSpace(getenv("ROUTEUP_TOKEN")), cc.Token)
	return server, token
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
