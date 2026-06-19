// Package state resolves filesystem paths used by routeup at runtime.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// AgentSocketPath returns the path at which the local agent listens for CLI IPC
func AgentSocketPath() (string, error) {
	if v := os.Getenv(AgentSocketEnv); v != "" {
		return v, nil
	}

	if runtime.GOOS == "linux" {
		if xdg := os.Getenv(XDGRuntimeEnv); xdg != "" {
			return filepath.Join(xdg, XDGSubdir, AgentSocketName), nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	if home == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, StateDirName, AgentSocketName), nil
}

// AgentLogPath returns the file the spawned agent writes stdout and stderr to.
func AgentLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, StateDirName, AgentLogName), nil
}

// AgentPIDPath returns the path of the file holding the running agent's PID.
func AgentPIDPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, StateDirName, AgentPIDName), nil
}

// EnsureParentDir creates the parent directory of path (mode 0700) if needed.
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}
