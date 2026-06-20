//go:build darwin

package certs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func installTrust(ctx context.Context, certPath string, opts TrustOptions) error {
	if opts.System {
		// Drop any stale CA of the same name so trusted copies don't pile up
		// across re-runs, then add the fresh one.
		deleteCertsByName(ctx, caCommonName, "/Library/Keychains/System.keychain", true)
		cmd := exec.CommandContext(ctx, "sudo",
			"security", "add-trusted-cert",
			"-d",
			"-r", "trustRoot",
			"-k", "/Library/Keychains/System.keychain",
			certPath)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("security add-trusted-cert (system keychain): %w", err)
		}
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home dir: %w", err)
	}
	keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	deleteCertsByName(ctx, caCommonName, keychain, false)
	cmd := exec.CommandContext(ctx, "security", "add-trusted-cert",
		"-r", "trustRoot",
		"-k", keychain,
		certPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("security add-trusted-cert (login keychain): %w", err)
	}
	return nil
}

// verifyTrust shells out to `security verify-cert`. Exit 0 = trusted.
func verifyTrust(certPath string) (bool, error) {
	cmd := exec.Command("security", "verify-cert", "-c", certPath)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("security verify-cert: %w", err)
}

// uninstallTrust removes the routeup CA from the keychains. It first clears
// the trust-settings record for the current CA (user + admin domains) so no
// orphaned setting lingers once the cert is gone, then deletes the cert(s)
// by SHA-1 hash — duplicates from repeated setups are all removed (deleting
// by name fails when more than one matches, "ambiguous").
func uninstallTrust(ctx context.Context, certPath string) error {
	if certPath != "" {
		removeTrustSetting(ctx, certPath, false)
		removeTrustSetting(ctx, certPath, true)
	}
	if home, err := os.UserHomeDir(); err == nil {
		login := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		deleteCertsByName(ctx, caCommonName, login, false)
	}
	deleteCertsByName(ctx, caCommonName, "/Library/Keychains/System.keychain", true)
	return nil
}

// removeTrustSetting clears the trust-settings record for the cert at
// certPath. admin=true targets the system domain (sudo). Best-effort.
func removeTrustSetting(ctx context.Context, certPath string, admin bool) {
	var cmd *exec.Cmd
	if admin {
		cmd = exec.CommandContext(ctx, "sudo", "security", "remove-trusted-cert", "-d", certPath)
	} else {
		cmd = exec.CommandContext(ctx, "security", "remove-trusted-cert", certPath)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	_ = cmd.Run()
}

// deleteCertsByName deletes every cert with common name cn from keychain.
func deleteCertsByName(ctx context.Context, cn, keychain string, sudo bool) {
	for _, hash := range certHashes(ctx, cn, keychain) {
		args := []string{"security", "delete-certificate", "-Z", hash, keychain}
		var cmd *exec.Cmd
		if sudo {
			cmd = exec.CommandContext(ctx, "sudo", args...)
		} else {
			cmd = exec.CommandContext(ctx, args[0], args[1:]...)
		}
		cmd.Stdin = os.Stdin
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		_ = cmd.Run()
	}
}

// certHashes returns the SHA-1 hashes of all certs in keychain matching cn.
// No sudo: reading certs is unprivileged even for the system keychain.
func certHashes(ctx context.Context, cn, keychain string) []string {
	out, err := exec.CommandContext(ctx, "security",
		"find-certificate", "-a", "-c", cn, "-Z", keychain).Output()
	if err != nil {
		return nil
	}
	var hashes []string
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "SHA-1 hash: "); ok {
			hashes = append(hashes, strings.TrimSpace(rest))
		}
	}
	return hashes
}
