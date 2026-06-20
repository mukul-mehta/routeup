// Package privbind installs the per-OS machinery for binding privileged
// ports. Linux uses setcap on the binary; macOS installs a LaunchDaemon
// root forwarder. Setup needs sudo once; runtime needs no privileges.
package privbind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Health is the result of a Check.
type Health int

const (
	HealthOK Health = iota
	HealthWarn
	HealthFail
)

// Required reports whether userPort needs privileged-bind machinery.
func Required(userPort int) bool {
	return userPort < 1024
}

// Install configures the OS to allow binding userPort. Idempotent.
// Prompts for sudo; pass a ctx with a 60s+ timeout.
func Install(ctx context.Context, userPort int) error {
	return install(ctx, userPort)
}

// Uninstall removes whatever Install set up (the macOS LaunchDaemon or the
// Linux capability). Best-effort and idempotent; prompts for sudo.
func Uninstall(ctx context.Context) error {
	return uninstall(ctx)
}

// Check reports the health of the privileged-bind setup for userPort.
// configuredBinPath is the binary path setup recorded (from the marker);
// pass "" if unknown.
func Check(userPort int, configuredBinPath string) (Health, string) {
	return check(userPort, configuredBinPath)
}

// AgentBindPort returns the port the agent listens on. macOS keeps the
// agent on the internal high port (the LaunchDaemon forwards); Linux binds
// the user port directly.
func AgentBindPort(userPort int) int {
	return agentBindPort(userPort)
}

// BinaryPath returns a stable path to the current binary for embedding in a
// LaunchDaemon plist or the setup marker. Symlinks are NOT resolved: on
// Homebrew the stable symlink (/opt/homebrew/bin/routeup) survives upgrades
// while the versioned Cellar path does not.
func BinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate binary: %w", err)
	}
	return filepath.Abs(exe)
}
