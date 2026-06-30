package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mukul-mehta/routeup/internal/route"
)

func parseTargetFlags(values []string) ([]route.Target, error) {
	if len(values) == 0 {
		return nil, nil
	}
	targets := make([]route.Target, 0, len(values))
	for _, value := range values {
		path, portText, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --target %q (use /path=port)", value)
		}
		port, err := strconv.Atoi(strings.TrimSpace(portText))
		if err != nil {
			return nil, fmt.Errorf("invalid --target %q: %w", value, err)
		}
		target, err := route.NormalizeTarget(route.Target{Path: path, Port: port})
		if err != nil {
			return nil, fmt.Errorf("invalid --target %q: %w", value, err)
		}
		targets = append(targets, target)
	}
	return route.NormalizeTargets(targets)
}

func printTargets(out io.Writer, targets []route.Target) {
	_, _ = fmt.Fprintln(out, "targets:")
	for _, target := range targets {
		_, _ = fmt.Fprintf(out, "  %-8s http://localhost:%d\n", target.Path, target.Port)
	}
}

func formatTargets(targets []route.Target) string {
	if len(targets) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		parts = append(parts, fmt.Sprintf("%s:%d", target.Path, target.Port))
	}
	return strings.Join(parts, ",")
}

func formatExposePaths(paths []string) string {
	if len(paths) == 0 {
		return "all paths"
	}
	return strings.Join(paths, ",")
}
