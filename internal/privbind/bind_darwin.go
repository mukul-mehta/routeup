//go:build darwin

package privbind

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

const (
	plistPath  = "/Library/LaunchDaemons/dev.routeup.forwarder.plist"
	plistLabel = "dev.routeup.forwarder"
	stdLogPath = "/var/log/routeup-forwarder.log"
)

// install writes a LaunchDaemon plist that runs `routeup forward
// 127.0.0.1:userPort 127.0.0.1:47443` as root, then bootstraps it via
// launchctl. One sudo prompt covers both calls. Idempotent on rerun.
func install(ctx context.Context, userPort int) error {
	if userPort >= 1024 {
		return nil // no privileged bind needed
	}
	if userPort == ipc.DefaultTLSPort {
		return nil // user port already equals internal port; no forwarder
	}

	bin, err := BinaryPath()
	if err != nil {
		return err
	}

	plist := renderPlist(bin, userPort, ipc.DefaultTLSPort)
	if err := sudoWriteFile(ctx, plistPath, plist); err != nil {
		return fmt.Errorf("write %s: %w", plistPath, err)
	}

	// bootout-then-bootstrap is the idempotent reload pattern. bootout
	// errors loudly when nothing's loaded ("Boot-out failed: 5: Input/output
	// error"); silence its stderr so the user doesn't see expected noise.
	// bootstrap's stderr stays inherited so real failures surface.
	_ = runSudoQuiet(ctx, "launchctl", "bootout", "system", plistPath)
	if err := runSudo(ctx, "launchctl", "bootstrap", "system", plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w", plistLabel, err)
	}
	return nil
}

// uninstall boots out and removes the LaunchDaemon plist. No-op if absent.
func uninstall(ctx context.Context) error {
	if _, err := os.Stat(plistPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_ = runSudoQuiet(ctx, "launchctl", "bootout", "system", plistPath)
	if err := runSudo(ctx, "rm", "-f", plistPath); err != nil {
		return fmt.Errorf("remove %s: %w", plistPath, err)
	}
	return nil
}

// check verifies the forwarder is installed and points at an existing binary.
// File-based (no sudo): the plist is world-readable and the binary path comes
// from the marker.
func check(userPort int, configuredBinPath string) (Health, string) {
	if userPort >= 1024 || userPort == ipc.DefaultTLSPort {
		return HealthOK, fmt.Sprintf("port %d: serves directly, no helper needed", userPort)
	}
	if _, err := os.Stat(plistPath); err != nil {
		return HealthFail, fmt.Sprintf("port %d not set up — run `routeup setup`", userPort)
	}
	if configuredBinPath != "" {
		if _, err := os.Stat(configuredBinPath); err != nil {
			return HealthFail, "the port 443 helper points at a missing binary (after an upgrade?) — run `routeup setup`"
		}
	}
	return HealthOK, fmt.Sprintf("port %d: helper installed", userPort)
}

// renderPlist emits the plist. RunAtLoad + KeepAlive: start at boot, restart on exit.
func renderPlist(binaryPath string, userPort, internalPort int) []byte {
	fromAddr := "127.0.0.1:" + strconv.Itoa(userPort)
	toAddr := "127.0.0.1:" + strconv.Itoa(internalPort)

	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>forward</string>
        <string>%s</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, plistLabel, binaryPath, fromAddr, toAddr, stdLogPath, stdLogPath)
}

// sudoWriteFile pipes content into `sudo tee path`. Result is root-owned, 0644.
func sudoWriteFile(ctx context.Context, path string, content []byte) error {
	cmd := exec.CommandContext(ctx, "sudo", "tee", path)
	cmd.Stdin = bytes.NewReader(content)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runSudo(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// runSudoQuiet is runSudo with stdout/stderr discarded. Use when failure
// is tolerated (e.g. bootout on first install).
func runSudoQuiet(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// agentBindPort returns 47443 for privileged user ports (the LaunchDaemon
// forwards); the user port directly otherwise.
func agentBindPort(userPort int) int {
	if userPort < 1024 {
		return ipc.DefaultTLSPort
	}
	return userPort
}
