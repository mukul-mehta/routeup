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

// planLinuxInstall detects the distro family from path existence.
func planLinuxInstall() (linuxInstallPlan, error) {
	if _, err := os.Stat("/etc/pki/ca-trust/source/anchors"); err == nil {
		return linuxInstallPlan{
			anchorDir:   "/etc/pki/ca-trust/source/anchors",
			refreshCmd:  "update-ca-trust",
			refreshArgs: []string{"extract"},
		}, nil
	}
	if _, err := os.Stat("/usr/local/share/ca-certificates"); err == nil {
		return linuxInstallPlan{
			anchorDir:  "/usr/local/share/ca-certificates",
			refreshCmd: "update-ca-certificates",
		}, nil
	}
	return linuxInstallPlan{}, errors.New(
		"unsupported linux distribution: neither " +
			"/etc/pki/ca-trust/source/anchors (rhel-family) nor " +
			"/usr/local/share/ca-certificates (debian-family) exists")
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
