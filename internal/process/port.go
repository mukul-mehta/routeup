package process

import (
	"errors"
	"fmt"
	"net"
	"syscall"
)

// FreePort returns a currently unused loopback port. Callers must tolerate a
// bind race after it returns.
func FreePort() (int, error) {
	for {
		ipv4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return 0, fmt.Errorf("reserve IPv4 port: %w", err)
		}
		port := ipv4.Addr().(*net.TCPAddr).Port

		ipv6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
		if err == nil {
			_ = ipv6.Close() // The listener only verifies that the port is free.
			_ = ipv4.Close()
			return port, nil
		}
		_ = ipv4.Close()
		if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) {
			return port, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return 0, fmt.Errorf("reserve IPv6 port %d: %w", port, err)
		}
	}
}

// EnsurePortAvailable reports whether a child can bind port on loopback.
func EnsurePortAvailable(port int) error {
	ipv4, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is unavailable on IPv4 loopback: %w", port, err)
	}
	defer func() { _ = ipv4.Close() }()

	ipv6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err == nil {
		_ = ipv6.Close()
		return nil
	}
	if errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) {
		return nil
	}
	return fmt.Errorf("port %d is unavailable on IPv6 loopback: %w", port, err)
}
