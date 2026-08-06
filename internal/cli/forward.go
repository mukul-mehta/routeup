package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
)

// newForwardCmd builds the hidden `routeup forward <from>... <to>` subcommand,
// a TCP byte-pipe used only by the macOS LaunchDaemon. Accepts one or more
// from-addrs (all but the last arg); the final arg is the target. Refuses
// non-loopback targets at startup.
func newForwardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "forward <from-addr> [<from-addr>...] <to-addr>",
		Short:  "(internal) tcp forwarder used by the macOS launchd plist",
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runForward(cmd, args[:len(args)-1], args[len(args)-1])
		},
	}
}

func runForward(cmd *cobra.Command, fromAddrs []string, toAddr string) error {
	if err := validateLoopback(toAddr); err != nil {
		return fmt.Errorf("invalid forwarding target %q: %w", toAddr, err)
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var listeners []net.Listener
	for _, fromAddr := range fromAddrs {
		l, err := net.Listen("tcp", fromAddr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return fmt.Errorf("bind %s: %w", fromAddr, err)
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "routeup forwarder: %s -> %s\n", l.Addr(), toAddr)
		listeners = append(listeners, l)
	}

	// Close all listeners on shutdown so Accept returns.
	go func() {
		<-ctx.Done()
		for _, l := range listeners {
			_ = l.Close()
		}
	}()

	var wg sync.WaitGroup
	for _, l := range listeners {
		wg.Add(1)
		go func(l net.Listener) {
			defer wg.Done()
			for {
				conn, err := l.Accept()
				if err != nil {
					if ctx.Err() == nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "routeup forwarder: accept on %s: %v\n", l.Addr(), err)
					}
					return
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					handleForward(conn, toAddr, cmd.ErrOrStderr())
				}()
			}
		}(l)
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

func handleForward(client net.Conn, toAddr string, logOut io.Writer) {
	defer func() { _ = client.Close() }()

	upstream, err := net.Dial("tcp", toAddr)
	if err != nil {
		_, _ = fmt.Fprintf(logOut, "routeup forwarder: dial upstream %s: %v\n", toAddr, err)
		return
	}
	defer func() { _ = upstream.Close() }()

	// First Copy to finish closes both ends and unblocks the other.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
}

// validateLoopback requires a literal loopback IP (no hostnames) so the
// forwarder can't be repurposed via attacker-controlled argv.
func validateLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("split host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return errors.New("host must be a literal loopback IP (use 127.0.0.1 or ::1)")
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("host %s is not a loopback address (use 127.0.0.1 or ::1)", host)
	}
	return nil
}
