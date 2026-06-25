//go:build linux

package certs

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// installTrust copies the CA into the distro anchor dir and runs the
// distro's refresh tool (both via sudo).
func installTrust(ctx context.Context, certPath string, _ TrustOptions) error {
	plan, err := planLinuxInstall()
	if err != nil {
		return err
	}
	dst := filepath.Join(plan.anchorDir, "routeup-ca.crt")

	if err := runSudo(ctx, "cp", certPath, dst); err != nil {
		return fmt.Errorf("copy ca to %s: %w", dst, err)
	}
	refreshArgs := append([]string{plan.refreshCmd}, plan.refreshArgs...)
	if err := runSudo(ctx, refreshArgs...); err != nil {
		return fmt.Errorf("refresh ca trust via %s: %w", plan.refreshCmd, err)
	}
	return nil
}

// uninstallTrust removes the anchor file and refreshes the trust store.
func uninstallTrust(ctx context.Context, _ string) error {
	plan, err := planLinuxInstall()
	if err != nil {
		return err
	}
	dst := filepath.Join(plan.anchorDir, "routeup-ca.crt")
	if err := runSudo(ctx, "rm", "-f", dst); err != nil {
		return fmt.Errorf("remove %s: %w", dst, err)
	}
	refreshArgs := append([]string{plan.refreshCmd}, plan.refreshArgs...)
	if err := runSudo(ctx, refreshArgs...); err != nil {
		return fmt.Errorf("refresh ca trust via %s: %w", plan.refreshCmd, err)
	}
	return nil
}

type linuxInstallPlan struct {
	anchorDir   string
	refreshCmd  string
	refreshArgs []string
}

// linuxTrustStores are the per-distro-family CA anchor dirs and their refresh
// command, in detection order: the first whose anchorDir exists wins. Paths and
// commands track mkcert's matrix.
var linuxTrustStores = []linuxInstallPlan{
	// RHEL / Fedora / CentOS.
	{anchorDir: "/etc/pki/ca-trust/source/anchors", refreshCmd: "update-ca-trust", refreshArgs: []string{"extract"}},
	// Debian / Ubuntu.
	{anchorDir: "/usr/local/share/ca-certificates", refreshCmd: "update-ca-certificates"},
	// Arch (p11-kit). update-ca-trust there is just a wrapper around
	// `trust extract-compat` and isn't always installed, so call trust(1).
	{anchorDir: "/etc/ca-certificates/trust-source/anchors", refreshCmd: "trust", refreshArgs: []string{"extract-compat"}},
}

// planLinuxInstall picks the trust store for this machine by anchor-dir existence.
func planLinuxInstall() (linuxInstallPlan, error) {
	return selectLinuxTrustStore(linuxTrustStores, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// selectLinuxTrustStore returns the first store whose anchorDir reports present.
// The exists probe is injected so the selection is unit-testable off-distro.
func selectLinuxTrustStore(stores []linuxInstallPlan, exists func(string) bool) (linuxInstallPlan, error) {
	for _, s := range stores {
		if exists(s.anchorDir) {
			return s, nil
		}
	}
	dirs := make([]string, len(stores))
	for i, s := range stores {
		dirs[i] = s.anchorDir
	}
	return linuxInstallPlan{}, fmt.Errorf(
		"unsupported linux distribution: none of the known CA trust anchor directories exist (%s)",
		strings.Join(dirs, ", "))
}

func runSudo(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// verifyTrust checks that the cert chains to a root in x509.SystemCertPool.
func verifyTrust(certPath string) (bool, error) {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return false, fmt.Errorf("read cert: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return false, errors.New("decode cert pem: not a CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("parse cert: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return false, fmt.Errorf("read system cert pool: %w", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool}); err == nil {
		return true, nil
	} else {
		var unknown x509.UnknownAuthorityError
		if errors.As(err, &unknown) {
			return false, nil
		}
		return false, err
	}
}
