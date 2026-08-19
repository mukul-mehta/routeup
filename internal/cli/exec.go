package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/process"
	"github.com/mukul-mehta/routeup/internal/state"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [-- <command> [args...]]",
		Short: "Run a command with the Routeup project environment",
		Long: "Run one command with Routeup's local URL and CA environment without\n" +
			"starting the agent, registering a route, or creating a public exposure.\n\n" +
			"With no command, exec uses the configured package.json script or\n" +
			"routeup.json command. Put an explicit command after --.",
		Example: "  routeup serve\n" +
			"  routeup exec -- yarn start:dev\n" +
			"  routeup exec -- yarn start:consumer:sync",
		Args: validateExecArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			return runExec(cmd, cwd, args)
		},
	}
	return cmd
}

func validateExecArgs(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 {
		if len(args) > 0 {
			return errors.New("put the command after -- (for example, `routeup exec -- yarn start:dev`)")
		}
		return nil
	}
	if dash != 0 {
		return errors.New("put all command arguments after --")
	}
	if len(args) == 0 {
		return errors.New("a command is required after --")
	}
	return nil
}

func runExec(cmd *cobra.Command, cwd string, argv []string) error {
	discovered, err := config.Discover(cwd)
	if err != nil {
		return fmt.Errorf("discover config: %w", err)
	}
	file := discovered.Config

	runner := process.Runner{Dir: cwd}
	if len(argv) > 0 {
		runner.Argv = append([]string(nil), argv...)
	} else {
		runner.Command = strings.TrimSpace(file.Command)
		if runner.Command == "" {
			return errors.New("nothing to execute: set \"script\" in your package.json routeup block, set \"command\" in routeup.json, or pass a command after --")
		}
	}

	routeName, err := runnerRoute(file, cwd)
	if err != nil {
		return err
	}
	if err := certs.EnsureLocalCA(); err != nil {
		return err
	}
	caCertPath, err := state.CACertPath()
	if err != nil {
		return err
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	local := localURL(routeName.LocalHost(), state.TLSPortOrDefault())
	public := activeExecPublicURL(parent, cmd.Root().Version, routeName.String())
	port, err := execConfiguredPort(file)
	if err != nil {
		return fmt.Errorf("resolve exec environment: %w", err)
	}
	runner.Env = process.InjectEnv(os.Environ(), process.EnvInputs{
		Port:       port,
		PortEnvVar: file.PortEnvVar,
		Host:       "127.0.0.1",
		LocalURL:   local,
		PublicURL:  public,
		CACertPath: caCertPath,
		WorkDir:    cwd,
	})

	ctx, stop := process.NotifyContext(parent)
	defer stop()
	code, err := runner.Run(ctx, process.Stdio{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("exec command: %w", err)
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

func execConfiguredPort(file config.Config) (int, error) {
	hasTargets := file.Port != 0 || len(file.Targets) > 0 || strings.TrimSpace(os.Getenv("ROUTEUP_PORT")) != ""
	if !hasTargets {
		return 0, nil
	}
	_, port, err := config.ResolveTargets(config.Inputs{Env: os.Getenv, File: file})
	return port, err
}

func activeExecPublicURL(parent context.Context, version, routeName string) string {
	socketPath, err := state.AgentSocketPath()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	status, err := agentctl.NewClient(socketPath, "", version).Status(ctx)
	if err != nil {
		return ""
	}
	for _, exposure := range status.Exposures {
		if exposure.Route == routeName && exposure.Host != "" {
			return "https://" + exposure.Host
		}
	}
	return ""
}
