// Package config holds the per-service routeup configuration types and the
// loaders for routeup.json and the package.json "routeup" block.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mukul-mehta/routeup/internal/route"
)

// Config holds settings loaded from a routeup.json or a package.json
// "routeup" block. Zero values mean "unset" and are resolved later by the
// precedence chain. Use Load* functions to populate; Validate at load time.
type Config struct {
	// Name is the service route name (e.g. "myapp"). Empty when unset.
	Name string `json:"name,omitempty"`

	// Port is shorthand for a root target at "/". Zero when unset.
	Port int `json:"port,omitempty"`

	// Targets are path-routed local upstreams behind this route.
	Targets []route.Target `json:"targets,omitempty"`

	// Expose configures public exposure for this route.
	Expose ExposeConfig `json:"expose,omitempty"`

	// Capture configures per-direction request/response retention for inspect.
	// Disabled by default because retained data may contain secrets.
	Capture CaptureConfig `json:"capture,omitempty"`

	// Script names a package.json script to resolve in runner mode.
	Script string `json:"script,omitempty"`

	// Command is the resolved shell command run in runner mode.
	Command string `json:"command,omitempty"`
}

// CaptureConfig controls which parts of each request/response exchange routeup
// retains for `routeup inspect`. All fields default to false (no capture).
type CaptureConfig struct {
	// Request retains incoming request headers and body.
	Request bool `json:"request,omitempty"`

	// Response retains upstream response headers and body.
	Response bool `json:"response,omitempty"`

	// RedactHeaders lists case-insensitive header names to omit from both the
	// retained request and response while still forwarding them upstream.
	RedactHeaders []string `json:"redact_headers,omitempty"`
}

// ExposeConfig holds public exposure constraints loaded from config.
type ExposeConfig struct {
	// Enabled opts bare `routeup` (runner mode) into public exposure before
	// child launch. When true, the runner contacts the configured server,
	// claims a public route, and injects the public URL into ROUTEUP_URL.
	// Has no effect on standalone `routeup serve` or `routeup expose`.
	Enabled bool `json:"enabled,omitempty"`

	// Paths limits which request paths are exposed publicly. Empty means all paths.
	Paths []string `json:"paths,omitempty"`
}

// LoadRouteupJSON reads and decodes a routeup.json file at path
func LoadRouteupJSON(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("could not parse %s: %w", path, err)
	}

	if c.Script != "" {
		return Config{}, fmt.Errorf("%s: script is only valid in a package.json routeup block; use \"command\" here", path)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("could not validate %s: %w", path, err)
	}

	return c, nil
}

// Validate enforces field-level rules on a Config:
//   - Name, if non-empty, must parse via route.Parse.
//   - Port, if non-zero, must be in [1, 65535].
//   - Targets, if non-empty, must have valid unique path prefixes and ports.
//   - Expose paths, if non-empty, must be valid public path patterns.
//   - RedactHeaders, if set, must contain valid HTTP header names.
//   - Script and Command cannot both be set.
func (c Config) Validate() error {
	if c.Name != "" {
		if _, err := route.Parse(c.Name); err != nil {
			return fmt.Errorf("invalid name: %w", err)
		}
	}

	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("port %d out of range [1, 65535]", c.Port)
	}

	targets, err := route.NormalizeTargets(c.Targets)
	if err != nil {
		return fmt.Errorf("invalid targets: %w", err)
	}
	if c.Port != 0 {
		for _, target := range targets {
			if target.Path == "/" {
				return fmt.Errorf("port and targets path %q both configure the root target", target.Path)
			}
		}
	}

	if _, err := route.NormalizePathPatterns(c.Expose.Paths); err != nil {
		return fmt.Errorf("invalid expose paths: %w", err)
	}
	if err := validateRedactHeaders(c.Capture.RedactHeaders); err != nil {
		return err
	}

	if c.Script != "" && c.Command != "" {
		return errors.New("set either script or command, not both")
	}

	return nil
}

func validateRedactHeaders(headers []string) error {
	for _, raw := range headers {
		name := strings.TrimSpace(raw)
		if name == "" {
			return errors.New("redact_headers contains an empty header name")
		}
		if !validHeaderName(name) {
			return fmt.Errorf("invalid header name %q in redact_headers", raw)
		}
	}
	return nil
}

func validHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		if !isHeaderTokenByte(name[i]) {
			return false
		}
	}
	return true
}

func isHeaderTokenByte(b byte) bool {
	if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}
