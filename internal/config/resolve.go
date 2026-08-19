package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mukul-mehta/routeup/internal/route"
)

// Inputs holds the sources Resolve consults, ordered by precedence within each field
type Inputs struct {
	PositionalName string
	PortFlag       int
	TargetFlags    []route.Target
	Env            func(string) string
	File           Config
	// DirName is the working-directory basename used as a last-resort route
	// name when no name is set via positional, ROUTEUP_NAME, or File.Name.
	// Callers set this to filepath.Base(cwd); it is validated by route.Parse.
	DirName string
}

// Resolved is the final, validated route + target.
type Resolved struct {
	Route   route.Name
	Port    int
	Targets []route.Target
}

// Resolve applies precedence:
//
//	Targets: File port/targets, overridden per path by ROUTEUP_PORT, --port,
//	         and --target. Errors if all are unset. Out-of-range values error.
//
//	Name: PositionalName (literal) > ROUTEUP_NAME env > File.Name.
//	      Errors if all are empty.
func Resolve(in Inputs) (Resolved, error) {
	targets, err := resolveTargets(in)
	if err != nil {
		return Resolved{}, err
	}

	parsed, err := ResolveName(in)
	if err != nil {
		return Resolved{}, err
	}

	return Resolved{Route: parsed, Port: route.PrimaryPort(targets), Targets: targets}, nil
}

// ResolveName applies positional/env/file precedence, then parses the result as
// a literal route name.
func ResolveName(in Inputs) (route.Name, error) {
	nameStr, err := resolveName(in)
	if err != nil {
		return route.Name{}, err
	}
	parsed, err := route.Parse(nameStr)
	if err != nil {
		return route.Name{}, fmt.Errorf("invalid route name: %w", err)
	}
	return parsed, nil
}

// ResolveTargets resolves only the target set. It is used by `routeup expose`,
// where the public route name is resolved separately from local target config.
func ResolveTargets(in Inputs) ([]route.Target, int, error) {
	targets, err := resolveTargets(in)
	if err != nil {
		return nil, 0, err
	}
	return targets, route.PrimaryPort(targets), nil
}

func resolveTargets(in Inputs) ([]route.Target, error) {
	targets := make([]route.Target, 0, len(in.File.Targets)+len(in.TargetFlags)+1)
	var err error

	if in.File.Port != 0 {
		targets, err = setTarget(targets, route.Target{Path: "/", Port: in.File.Port})
		if err != nil {
			return nil, err
		}
	}
	for _, target := range in.File.Targets {
		targets, err = setTarget(targets, target)
		if err != nil {
			return nil, err
		}
	}

	if raw := envGet(in.Env, "ROUTEUP_PORT"); raw != "" {
		trimmed := strings.TrimSpace(raw)
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid ROUTEUP_PORT %q: %w", raw, err)
		}
		targets, err = setTarget(targets, route.Target{Path: "/", Port: n})
		if err != nil {
			return nil, err
		}
	}

	if in.PortFlag != 0 {
		targets, err = setTarget(targets, route.Target{Path: "/", Port: in.PortFlag})
		if err != nil {
			return nil, err
		}
	}

	for _, target := range in.TargetFlags {
		targets, err = setTarget(targets, target)
		if err != nil {
			return nil, err
		}
	}

	if len(targets) == 0 {
		return nil, errors.New("no targets specified (use --port, --target, ROUTEUP_PORT, or set port/targets in config)")
	}
	return route.NormalizeTargets(targets)
}

func setTarget(targets []route.Target, target route.Target) ([]route.Target, error) {
	normalized, err := route.NormalizeTarget(target)
	if err != nil {
		return nil, err
	}
	for i, existing := range targets {
		existing, err := route.NormalizeTarget(existing)
		if err != nil {
			return nil, err
		}
		if existing.Path == normalized.Path {
			targets[i] = normalized
			return targets, nil
		}
	}
	return append(targets, normalized), nil
}

// resolveName picks a literal name string via positional > ROUTEUP_NAME env >
// File.Name > working-directory basename.
func resolveName(in Inputs) (string, error) {
	if in.PositionalName != "" {
		return in.PositionalName, nil
	}
	if envName := strings.TrimSpace(envGet(in.Env, "ROUTEUP_NAME")); envName != "" {
		return envName, nil
	}
	if in.File.Name != "" {
		return in.File.Name, nil
	}
	if in.DirName != "" {
		return in.DirName, nil
	}
	return "", errors.New("no route name (pass a positional, set ROUTEUP_NAME, set name in config, or run from a named directory)")
}

// envGet is a nil-safe wrapper around the injected env func.
func envGet(env func(string) string, key string) string {
	if env == nil {
		return ""
	}
	return env(key)
}
