//go:build linux

package privbind

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// install grants cap_net_bind_service on the routeup binary so the agent
// can bind privileged ports. Lost when the binary is replaced; user must
// rerun `routeup setup` after upgrades.
func install(ctx context.Context, userPort int) error {
	if userPort >= 1024 {
		return nil // unprivileged port, no setcap needed
	}

	real, err := realBinaryPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "sudo", "setcap", "cap_net_bind_service=+ep", real)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setcap on %s: %w", real, err)
	}
	return nil
}

// uninstall removes the capability from the binary. Best-effort.
func uninstall(ctx context.Context) error {
	real, err := realBinaryPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "sudo", "setcap", "-r", real)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run() // ignore: capability may already be absent
	return nil
}

// check uses getcap to confirm the current binary can still bind privileged
// ports. After a binary replacement (upgrade) the capability is gone.
func check(userPort int, _ string) (Health, string) {
	if userPort >= 1024 {
		return HealthOK, fmt.Sprintf("port %d: serves directly, no setcap needed", userPort)
	}
	real, err := realBinaryPath()
	if err != nil {
		return HealthWarn, fmt.Sprintf("couldn't locate binary to check port %d: %v", userPort, err)
	}
	out, err := exec.Command("getcap", real).Output()
	if err != nil {
		return HealthWarn, fmt.Sprintf("couldn't check port %d binding (getcap: %v)", userPort, err)
	}
	if strings.Contains(string(out), "cap_net_bind_service") {
		return HealthOK, fmt.Sprintf("port %d: binary can bind privileged ports", userPort)
	}
	return HealthFail, fmt.Sprintf("binary can't bind port %d (lost after an upgrade?) — run `routeup setup`", userPort)
}

// realBinaryPath resolves symlinks: setcap and getcap operate on the inode,
// so they need the real file, not a Homebrew/symlink wrapper.
func realBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate binary: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return real, nil
}

// agentBindPort returns the user port directly; setcap lets us bind it.
func agentBindPort(userPort int) int {
	return userPort
}

// reapplyBind re-runs setcap on an explicit binary path (used after an
// update swaps the binary, when os.Executable() would point at the old,
// now-unlinked inode).
func reapplyBind(ctx context.Context, userPort int, binaryPath string) error {
	if userPort >= 1024 {
		return nil
	}
	real, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		real = binaryPath
	}
	cmd := exec.CommandContext(ctx, "sudo", "setcap", "cap_net_bind_service=+ep", real)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setcap on %s: %w", real, err)
	}
	return nil
}
