package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// pkgJSON is the minimal shape we decode from a package.json. Everything outside
// the "routeup" field is intentionally ignored.
type pkgJSON struct {
	Routeup json.RawMessage `json:"routeup"`
}

// LoadPackageJSON reads a package.json file at path and extracts the optional
// "routeup" block as a Config.
//
// Returns:
//   - (cfg, true,  nil)  — a "routeup" block was present and parses to a valid Config
//   - (zero, false, nil) — file exists but has no "routeup" block (or it is null)
//   - (zero, false, err) — read, parse, or validation error
//
// Scope prefixes on Name (e.g. "@org/foo") are stripped before validation.
// Unknown JSON fields outside the routeup block are tolerated.
func LoadPackageJSON(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false, fmt.Errorf("reading %s: %w", path, err)
	}

	var pkg pkgJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Config{}, false, fmt.Errorf("could not parse %s: %w", path, err)
	}

	if isAbsentRouteupBlock(pkg.Routeup) {
		return Config{}, false, nil
	}

	var c Config
	if err := json.Unmarshal(pkg.Routeup, &c); err != nil {
		return Config{}, false, fmt.Errorf("could not parse routeup block in %s: %w", path, err)
	}

	c.Name = stripScope(c.Name)

	if err := c.Validate(); err != nil {
		return Config{}, false, fmt.Errorf("could not validate %s: %w", path, err)
	}

	return c, true, nil
}

func isAbsentRouteupBlock(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func stripScope(name string) string {
	if !strings.HasPrefix(name, "@") {
		return name
	}
	i := strings.Index(name, "/")
	if i == -1 {
		return name
	}
	return name[i+1:]
}
