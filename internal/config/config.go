// Package config holds the per-service routeup configuration types and the
// loaders for routeup.json and the package.json "routeup" block.
package config

import (
	"encoding/json"
	"fmt"
	"os"

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
}

// ExposeConfig holds public exposure constraints loaded from config.
type ExposeConfig struct {
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

	return nil
}
