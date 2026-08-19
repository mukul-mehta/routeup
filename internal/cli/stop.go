package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/state"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a routeup serve owner",
		Long: "Stop an active route held by `routeup serve`, including a detached route.\n\n" +
			"This releases the local route and public exposure. It does not stop the\n" +
			"external application listening on the target port.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runStop(cmd, args, cwd)
		},
	}
	cmd.ValidArgsFunction = completeActiveRoutes
	return cmd
}

func runStop(cmd *cobra.Command, args []string, cwd string) error {
	discovered, err := config.Discover(cwd)
	if err != nil {
		return err
	}
	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	name, err := config.ResolveName(config.Inputs{
		PositionalName: positional,
		Env:            os.Getenv,
		File:           discovered.Config,
		DirName:        filepath.Base(cwd),
	})
	if err != nil {
		return err
	}
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return err
	}
	client := agentctl.NewClient(socketPath, "", cmd.Root().Version)
	ctx, cancel := context.WithTimeout(cmd.Context(), 85*time.Second)
	defer cancel()
	found, err := stopRouteAfterReconcile(ctx, client, name.String(), 75*time.Second)
	out := cmd.OutOrStdout()
	styles := newTerminalStyles(out)
	if agentctl.IsUnavailable(err) {
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.muted("route not active:"), name.String())
		return nil
	}
	if err != nil {
		return fmt.Errorf("stop route %q: %w", name.String(), err)
	}
	if !found {
		_, _ = fmt.Fprintf(out, "%s %s\n", styles.muted("route not active:"), name.String())
		return nil
	}
	if err := waitForRouteStop(ctx, client, name.String()); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "%s %s\n", styles.success("route stopped:"), name.String())
	return nil
}

func stopRouteAfterReconcile(ctx context.Context, client *agentctl.Client, name string, budget time.Duration) (bool, error) {
	deadline := time.Now().Add(budget)
	for {
		found, err := client.StopRoute(ctx, name)
		if err == nil && found {
			return true, nil
		}
		controlUnavailable := errors.Is(err, agentctl.ErrRouteOwnerControlUnavailable)
		if err != nil && !agentctl.IsUnavailable(err) && !controlUnavailable {
			return false, err
		}
		live, ownerErr := hasLiveServeOwner(name)
		if ownerErr != nil {
			return false, ownerErr
		}
		if !live {
			if controlUnavailable {
				return false, err
			}
			return false, nil
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("serve owner for route %q is alive but its agent control is unavailable", name)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func hasLiveServeOwner(name string) (bool, error) {
	owners, err := state.LiveOwners()
	if err != nil {
		return false, fmt.Errorf("read route owner state: %w", err)
	}
	for _, owner := range owners {
		if owner.Route == name && owner.Kind == state.OwnerServe {
			return true, nil
		}
	}
	return false, nil
}

func waitForRouteStop(ctx context.Context, client *agentctl.Client, name string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		claims, err := client.List(ctx)
		if err != nil {
			if agentctl.IsUnavailable(err) {
				return nil
			}
			return fmt.Errorf("verify route stopped: %w", err)
		}
		active := false
		for _, claim := range claims {
			if claim.Name == name {
				active = true
				break
			}
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("route %q did not stop: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func completeActiveRoutes(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client := agentctl.NewClient(socketPath, "", cmd.Root().Version)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	claims, err := client.List(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(claims))
	for _, claim := range claims {
		names = append(names, claim.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
