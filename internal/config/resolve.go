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
//	Name: PositionalName (with bare-name rule) > ROUTEUP_NAME env > File.Name.
//	      Errors if both are empty.
//
// Bare-name rule: if PositionalName has no dot AND File.Name is non-empty,
// the final name is PositionalName + "." + File.Name (closest project becomes
// the suffix). Otherwise the chosen string is used literally and parsed via
// route.Parse.
func Resolve(in Inputs) (Resolved, error) {
	targets, err := resolveTargets(in)
	if err != nil {
		return Resolved{}, err
	}

	nameStr, err := resolveName(in)
	if err != nil {
		return Resolved{}, err
	}

	parsed, err := route.Parse(nameStr)
	if err != nil {
		return Resolved{}, fmt.Errorf("invalid route name: %w", err)
	}

	return Resolved{Route: parsed, Port: route.PrimaryPort(targets), Targets: targets}, nil
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

// resolveName picks a name string via positional (with bare-name rule) >
// ROUTEUP_NAME env (literal) > File.Name (literal).
func resolveName(in Inputs) (string, error) {
	if in.PositionalName != "" {
		return applyBareName(in.PositionalName, in.File.Name), nil
	}
	if envName := strings.TrimSpace(envGet(in.Env, "ROUTEUP_NAME")); envName != "" {
		return envName, nil
	}
	if in.File.Name != "" {
		return in.File.Name, nil
	}
	return "", errors.New("no route name (pass a positional, set ROUTEUP_NAME, or set name in config)")
}

// applyBareName implements the PLAN.md rule: if positional has no dot and a
// project name is in scope, the project becomes the suffix; otherwise the
// positional is used literally. No validation here — route.Parse is the
// authority and produces specific errors downstream.
func applyBareName(positional, project string) string {
	if strings.Contains(positional, ".") {
		return positional
	}
	if project == "" {
		return positional
	}
	return positional + "." + project
}

// envGet is a nil-safe wrapper around the injected env func.
func envGet(env func(string) string, key string) string {
	if env == nil {
		return ""
	}
	return env(key)
}
