//go:build darwin

package privbind

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

const (
	plistPath  = "/Library/LaunchDaemons/dev.routeup.forwarder.plist"
	plistLabel = "dev.routeup.forwarder"
	helperPath = "/Library/PrivilegedHelperTools/dev.routeup.forwarder"
	stdLogPath = "/var/log/routeup-forwarder.log"
)

// install copies the current binary to a root-owned helper path, writes a
// LaunchDaemon plist that runs `forward 127.0.0.1:userPort [::1]:userPort
// 127.0.0.1:47443`, then bootstraps it. Idempotent on rerun.
func install(ctx context.Context, userPort int) error {
	if userPort >= 1024 {
		return nil // no privileged bind needed
	}
	if userPort == ipc.DefaultTLSPort {
		return nil // user port already equals internal port; no forwarder
	}

	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate routeup binary: %w", err)
	}
	return installFrom(ctx, userPort, source)
}

func installFrom(ctx context.Context, userPort int, source string) error {
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve routeup binary: %w", err)
	}
	source = absoluteSource
	if source != helperPath {
		stagedHelper := helperPath + ".new"
		_ = runSudoQuiet(ctx, "rm", "-f", stagedHelper)
		if err := runSudo(ctx, "install", "-o", "root", "-g", "wheel", "-m", "0755", source, stagedHelper); err != nil {
			return fmt.Errorf("install privileged helper: %w", err)
		}
		if err := runSudo(ctx, "mv", "-f", stagedHelper, helperPath); err != nil {
			return fmt.Errorf("replace privileged helper: %w", err)
		}
	}

	plist := renderPlist(helperPath, userPort, ipc.DefaultTLSPort)
	if err := sudoInstallFile(ctx, plistPath, plist); err != nil {
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
	_, plistErr := os.Stat(plistPath)
	_, helperErr := os.Stat(helperPath)
	if errors.Is(plistErr, os.ErrNotExist) && errors.Is(helperErr, os.ErrNotExist) {
		return nil
	}
	if plistErr == nil {
		_ = runSudoQuiet(ctx, "launchctl", "bootout", "system", plistPath)
	}
	if err := runSudo(ctx, "rm", "-f", plistPath, plistPath+".new", helperPath, helperPath+".new"); err != nil {
		return fmt.Errorf("remove privileged helper: %w", err)
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
	helperInfo, err := os.Lstat(helperPath)
	if err != nil {
		return HealthFail, fmt.Sprintf("port %d helper binary is missing — run `routeup setup`", userPort)
	}
	if err := validateRootOwnedFile(helperInfo); err != nil {
		return HealthFail, fmt.Sprintf("port %d helper binary has unsafe ownership or permissions — run `routeup setup`", userPort)
	}
	if configuredBinPath != "" {
		match, err := sameFileContents(configuredBinPath, helperPath)
		if err != nil || !match {
			return HealthFail, fmt.Sprintf("port %d helper is from another routeup build — run `routeup setup`", userPort)
		}
	}
	plistInfo, err := os.Lstat(plistPath)
	if err != nil {
		return HealthFail, fmt.Sprintf("couldn't inspect the port %d helper configuration — run `routeup setup`", userPort)
	}
	if err := validateRootOwnedFile(plistInfo); err != nil {
		return HealthFail, fmt.Sprintf("port %d helper configuration has unsafe ownership or permissions — run `routeup setup`", userPort)
	}
	plist, err := os.ReadFile(plistPath)
	if err != nil {
		return HealthFail, fmt.Sprintf("couldn't read the port %d helper — run `routeup setup`", userPort)
	}
	if err := validateForwarderPlist(plist, userPort); err != nil {
		return HealthFail, fmt.Sprintf("port %d helper does not match the current configuration — run `routeup setup`", userPort)
	}
	return HealthOK, fmt.Sprintf("port %d: helper installed", userPort)
}

func validateRootOwnedFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("helper is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return errors.New("helper is not owned by root")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("helper is group or world writable")
	}
	return nil
}

func sameFileContents(first, second string) (bool, error) {
	firstHash, err := fileSHA256(first)
	if err != nil {
		return false, err
	}
	secondHash, err := fileSHA256(second)
	if err != nil {
		return false, err
	}
	return firstHash == secondHash, nil
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func validateForwarderPlist(plist []byte, userPort int) error {
	expected := renderPlist(helperPath, userPort, ipc.DefaultTLSPort)
	if !bytes.Equal(plist, expected) {
		return errors.New("plist does not match the current helper configuration")
	}
	return nil
}

// renderPlist emits the plist. RunAtLoad + KeepAlive: start at boot, restart on exit.
// Both IPv4 and IPv6 loopback addrs are forwarded so clients that prefer ::1
// (curl, browsers on macOS Ventura+) reach the agent just like 127.0.0.1 clients.
func renderPlist(binaryPath string, userPort, internalPort int) []byte {
	fromV4 := "127.0.0.1:" + strconv.Itoa(userPort)
	fromV6 := "[::1]:" + strconv.Itoa(userPort)
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
`, plistLabel, binaryPath, fromV4, fromV6, toAddr, stdLogPath, stdLogPath)
}

// sudoInstallFile installs generated content with explicit root ownership and
// non-writable permissions, replacing any prior destination or symlink.
func sudoInstallFile(ctx context.Context, path string, content []byte) error {
	temp, err := os.CreateTemp("", "routeup-forwarder-*.plist")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	stagedPath := path + ".new"
	_ = runSudoQuiet(ctx, "rm", "-f", stagedPath)
	if err := runSudo(ctx, "install", "-o", "root", "-g", "wheel", "-m", "0644", tempPath, stagedPath); err != nil {
		return err
	}
	return runSudo(ctx, "mv", "-f", stagedPath, path)
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

// reapplyBind refreshes the root-owned helper after a binary update.
func reapplyBind(ctx context.Context, userPort int, binaryPath string) error {
	if userPort >= 1024 || userPort == ipc.DefaultTLSPort {
		return nil
	}
	return installFrom(ctx, userPort, binaryPath)
}
