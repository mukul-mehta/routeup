package certs

import (
	"fmt"

	"github.com/mukul-mehta/routeup/internal/state"
)

// EnsureLocalCA resolves the default local CA paths and ensures the CA exists
// and is usable, creating it if needed. It is the precondition for any command
// that goes through the local agent (serve, expose), since the agent needs the
// CA for local HTTPS.
func EnsureLocalCA() error {
	certPath, err := state.CACertPath()
	if err != nil {
		return err
	}
	keyPath, err := state.CAKeyPath()
	if err != nil {
		return err
	}
	if _, err := EnsureCA(certPath, keyPath); err != nil {
		return fmt.Errorf("setup incomplete: %w", err)
	}
	return nil
}
