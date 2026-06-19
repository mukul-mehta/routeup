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
	Env            func(string) string
	File           Config
}

// Resolved is the final, validated route + target.
type Resolved struct {
	Route route.Name
	Port  int
}

// Resolve applies precedence:
//
//	Port: PortFlag > ROUTEUP_PORT env > File.Port.
//	      Errors if all are unset. Out-of-range values error.
//
//	Name: PositionalName (with bare-name rule) > ROUTEUP_NAME env > File.Name.
//	      Errors if both are empty.
//
// Bare-name rule: if PositionalName has no dot AND File.Name is non-empty,
// the final name is PositionalName + "." + File.Name (closest project becomes
// the suffix). Otherwise the chosen string is used literally and parsed via
// route.Parse.
func Resolve(in Inputs) (Resolved, error) {
	port, err := resolvePort(in)
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

	return Resolved{Route: parsed, Port: port}, nil
}

// resolvePort picks a port via PortFlag > ROUTEUP_PORT env > File.Port and
// range-checks the result.
func resolvePort(in Inputs) (int, error) {
	port, err := pickPort(in)
	if err != nil {
		return 0, err
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d out of range [1, 65535]", port)
	}
	return port, nil
}

// pickPort returns the chosen port without range-checking it.
func pickPort(in Inputs) (int, error) {
	if in.PortFlag != 0 {
		return in.PortFlag, nil
	}

	if raw := envGet(in.Env, "ROUTEUP_PORT"); raw != "" {
		trimmed := strings.TrimSpace(raw)
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, fmt.Errorf("invalid ROUTEUP_PORT %q: %w", raw, err)
		}
		return n, nil
	}

	if in.File.Port != 0 {
		return in.File.Port, nil
	}

	return 0, errors.New("no port specified (use --port, ROUTEUP_PORT, or set port in config)")
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
