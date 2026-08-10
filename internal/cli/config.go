package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/config"
	"github.com/mukul-mehta/routeup/internal/route"
)

type configView struct {
	Source            string         `json:"source"`
	Path              string         `json:"path,omitempty"`
	Route             string         `json:"route"`
	Targets           []route.Target `json:"targets,omitempty"`
	CommandConfigured bool           `json:"command_configured"`
	PortEnvVar        string         `json:"port_env_var,omitempty"`
	ExposureEnabled   bool           `json:"exposure_enabled"`
	ExposurePaths     []string       `json:"exposure_paths,omitempty"`
	CaptureRequest    bool           `json:"capture_request"`
	CaptureResponse   bool           `json:"capture_response"`
	RedactHeaders     []string       `json:"redact_headers,omitempty"`
	Server            string         `json:"server,omitempty"`
	TokenConfigured   bool           `json:"token_configured"`
}

func newConfigCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show discovered and resolved project configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting cwd: %w", err)
			}
			view, err := resolveConfigView(cwd)
			if err != nil {
				return err
			}
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(view); err != nil {
					return fmt.Errorf("write config json: %w", err)
				}
				return nil
			}
			return writeConfigView(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write resolved configuration as JSON")
	return cmd
}

func resolveConfigView(cwd string) (configView, error) {
	discovered, err := config.Discover(cwd)
	if err != nil {
		return configView{}, err
	}
	name, err := config.ResolveName(config.Inputs{
		Env: os.Getenv, File: discovered.Config, DirName: filepath.Base(cwd),
	})
	if err != nil {
		return configView{}, err
	}
	var targets []route.Target
	hasTargets := discovered.Config.Port != 0 || len(discovered.Config.Targets) != 0 || os.Getenv("ROUTEUP_PORT") != ""
	if hasTargets {
		targets, _, err = config.ResolveTargets(config.Inputs{Env: os.Getenv, File: discovered.Config})
		if err != nil {
			return configView{}, err
		}
	}
	server, token, err := resolveServerToken("", "")
	if err != nil {
		return configView{}, err
	}
	source := string(discovered.Source)
	if source == "" {
		source = "none"
	}
	return configView{
		Source: source, Path: discovered.Path, Route: name.String(), Targets: targets,
		CommandConfigured: discovered.Config.Command != "", PortEnvVar: discovered.Config.PortEnvVar,
		ExposureEnabled: discovered.Config.Expose.Enabled,
		ExposurePaths:   append([]string(nil), discovered.Config.Expose.Paths...),
		CaptureRequest:  discovered.Config.Capture.Request, CaptureResponse: discovered.Config.Capture.Response,
		RedactHeaders: append([]string(nil), discovered.Config.Capture.RedactHeaders...),
		Server:        server, TokenConfigured: token != "",
	}, nil
}

func writeConfigView(out io.Writer, view configView) error {
	if _, err := fmt.Fprintf(out, "source: %s\n", terminalEscapeString(view.Source)); err != nil {
		return err
	}
	if view.Path != "" {
		_, _ = fmt.Fprintf(out, "path: %s\n", terminalEscapeString(view.Path))
	}
	_, _ = fmt.Fprintf(out, "route: %s\n", terminalEscapeString(view.Route))
	if len(view.Targets) == 0 {
		_, _ = fmt.Fprintln(out, "targets: not configured")
	} else {
		_, _ = fmt.Fprintln(out, "targets:")
		for _, target := range view.Targets {
			_, _ = fmt.Fprintf(out, "  %-8s http://localhost:%d\n", terminalEscapeString(target.Path), target.Port)
		}
	}
	_, _ = fmt.Fprintf(out, "command: %s\n", map[bool]string{true: "configured", false: "not configured"}[view.CommandConfigured])
	if view.PortEnvVar != "" {
		_, _ = fmt.Fprintf(out, "port env: PORT,%s\n", terminalEscapeString(view.PortEnvVar))
	}
	_, _ = fmt.Fprintf(out, "exposure enabled: %t\n", view.ExposureEnabled)
	_, _ = fmt.Fprintf(out, "expose paths: %s\n", terminalEscapeString(formatExposePaths(view.ExposurePaths)))
	capture := "disabled"
	switch {
	case view.CaptureRequest && view.CaptureResponse:
		capture = "request,response"
	case view.CaptureRequest:
		capture = "request"
	case view.CaptureResponse:
		capture = "response"
	}
	_, _ = fmt.Fprintf(out, "capture: %s\n", capture)
	if len(view.RedactHeaders) != 0 {
		redacted := make([]string, len(view.RedactHeaders))
		for i, header := range view.RedactHeaders {
			redacted[i] = terminalEscapeString(header)
		}
		_, _ = fmt.Fprintf(out, "redact headers: %s\n", strings.Join(redacted, ","))
	}
	if view.Server == "" {
		_, _ = fmt.Fprintln(out, "server: not configured")
	} else {
		_, _ = fmt.Fprintf(out, "server: %s\n", terminalEscapeString(view.Server))
	}
	_, err := fmt.Fprintf(out, "token: %s\n", map[bool]string{true: "configured", false: "not configured"}[view.TokenConfigured])
	return err
}
