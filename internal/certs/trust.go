package certs

import (
	"context"
)

type TrustOptions struct {
	// System (macOS): use system keychain (sudo) instead of login (Touch ID).
	// Ignored on Linux.
	System bool
}

// InstallTrust adds the CA to the OS trust store. Idempotent.
// Per-OS impls in trust_{darwin,linux}.go.
func InstallTrust(ctx context.Context, certPath string, opts TrustOptions) error {
	return installTrust(ctx, certPath, opts)
}

// VerifyTrust reports whether the cert is in the OS trust store.
// (false, nil) means untrusted; (false, err) means the probe failed.
func VerifyTrust(certPath string) (bool, error) {
	return verifyTrust(certPath)
}

// UninstallTrust removes the routeup CA from the OS trust store.
// Best-effort and idempotent; per-OS impls in trust_{darwin,linux}.go.
func UninstallTrust(ctx context.Context, certPath string) error {
	return uninstallTrust(ctx, certPath)
}
