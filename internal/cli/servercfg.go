package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/mukul-mehta/routeup/internal/server"
)

// defaultServerConfigPath is the config file looked up when --config is omitted.
const defaultServerConfigPath = "routeup-server.json"

// resolveServerConfig layers configuration: defaults < config file < overrides.
//
// configPath is the explicit --config value (may be empty). When empty, the
// default file is tried and a missing file is not an error. When an explicit
// path is given but missing, that is an error. overrides carries the fields a
// command set via flags (zero fields are ignored).
func resolveServerConfig(configPath string, overrides server.ServerConfig) (server.ServerConfig, error) {
	cfg := server.DefaultServerConfig()

	path := configPath
	usingDefault := configPath == ""
	if usingDefault {
		path = defaultServerConfigPath
	}

	fileCfg, err := server.LoadServerConfig(path)
	switch {
	case err == nil:
		cfg = server.Overlay(cfg, fileCfg)
	case errors.Is(err, os.ErrNotExist) && usingDefault:
		// No default config file present; defaults plus flags only.
	case errors.Is(err, os.ErrNotExist):
		return server.ServerConfig{}, fmt.Errorf("config file %s not found", configPath)
	default:
		return server.ServerConfig{}, err
	}

	return server.Overlay(cfg, overrides), nil
}
