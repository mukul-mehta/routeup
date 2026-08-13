// Package state resolves filesystem paths used by routeup at runtime.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultDir is set only on development builds through -ldflags. Production
// builds leave it empty and use the normal per-user state directory.
var defaultDir string

// Dir returns the routeup state directory. ROUTEUP_STATE_DIR overrides the
// default ~/.routeup location and may be relative to the current directory.
func Dir() (string, error) {
	if configured := configuredStateDir(); configured != "" {
		dir, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve state directory: %w", err)
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, StateDirName), nil
}

// IsDirOverridden reports whether ROUTEUP_STATE_DIR selects an isolated state
// root instead of the normal per-user state directory.
func IsDirOverridden() bool {
	return configuredStateDir() != ""
}

func configuredStateDir() string {
	if configured := strings.TrimSpace(os.Getenv(StateDirEnv)); configured != "" {
		return configured
	}
	return strings.TrimSpace(defaultDir)
}

// AgentSocketPath returns the path at which the local agent listens for CLI IPC
func AgentSocketPath() (string, error) {
	if v := os.Getenv(AgentSocketEnv); v != "" {
		return v, nil
	}
	if IsDirOverridden() {
		dir, err := Dir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, AgentSocketName), nil
	}

	if runtime.GOOS == "linux" {
		if xdg := os.Getenv(XDGRuntimeEnv); xdg != "" {
			return filepath.Join(xdg, XDGSubdir, AgentSocketName), nil
		}
	}

	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AgentSocketName), nil
}

// AgentLogPath returns the file the spawned agent writes stdout and stderr to.
func AgentLogPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AgentLogName), nil
}

// AgentPIDPath returns the path of the file holding the running agent's PID.
func AgentPIDPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AgentPIDName), nil
}

// CACertPath returns the path of the local CA certificate file.
func CACertPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CACertName), nil
}

// CAKeyPath returns the path of the local CA private key file.
func CAKeyPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CAKeyName), nil
}

// EnsureParentDir creates the parent directory of path (mode 0700) if needed.
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}
