package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Discovered is the result of looking for a routeup config in a directory.
// When Source is SourceNone, Config is the zero value and Path is empty.
type Discovered struct {
	Config Config
	Path   string // absolute path to the file that populated Config
	Source Source
}

// Discover looks for a routeup config in startDir. It checks, in order:
//
//  1. routeup.json
//  2. package.json with a "routeup" block
//
// routeup.json wins when both exist in the directory. A package.json without
// a routeup block counts as "no config here". When nothing matches, Discover
// returns Discovered{Source: SourceNone} with a nil error.
//
// Errors from the loaders (parse failures, validation failures) are propagated;
// only os.ErrNotExist is treated as "not here".
func Discover(startDir string) (Discovered, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return Discovered{}, fmt.Errorf("resolving %s: %w", startDir, err)
	}

	rPath := filepath.Join(abs, "routeup.json")
	cfg, err := LoadRouteupJSON(rPath)
	if err == nil {
		return Discovered{Config: cfg, Path: rPath, Source: SourceRouteupJSON}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Discovered{}, err
	}

	pPath := filepath.Join(abs, "package.json")
	pkgCfg, hasBlock, pErr := LoadPackageJSON(pPath)
	if pErr != nil && !errors.Is(pErr, os.ErrNotExist) {
		return Discovered{}, pErr
	}
	if pErr == nil && hasBlock {
		return Discovered{Config: pkgCfg, Path: pPath, Source: SourcePackageJSON}, nil
	}

	return Discovered{Source: SourceNone}, nil
}
