package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestForward_ProxiesBytes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from upstream"))
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	// Pick a free local port for the forwarder by binding+closing.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	fwdAddr := probe.Addr().String()
	_ = probe.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newForwardCmd()
	cmd.SetArgs([]string{fwdAddr, upstreamURL.Host})
	cmd.SetErr(io.Discard)
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	waitForwarderReady(t, fwdAddr)

	// Disable keep-alive so the conn drops cleanly at ctx cancel.
	tr := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Transport: tr}

	resp, err := client.Get("http://" + fwdAddr)
	if err != nil {
		t.Fatalf("http.Get through forwarder: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	tr.CloseIdleConnections()

	if got, want := string(body), "hello from upstream"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("forwarder Execute returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forwarder did not exit after ctx cancel")
	}
}

func TestForward_RejectsNonLoopback(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"public-ip", "1.2.3.4:443"},
		{"google-dns", "8.8.8.8:80"},
		{"lan", "192.168.1.1:443"},
		{"hostname", "example.com:80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newForwardCmd()
			cmd.SetArgs([]string{"127.0.0.1:0", tc.target})
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			if err == nil {
				t.Errorf("expected error for non-loopback target %s, got nil", tc.target)
			}
			if err != nil && !strings.Contains(err.Error(), "loopback") {
				t.Errorf("error = %v, want substring 'loopback'", err)
			}
		})
	}
}

// waitForwarderReady polls until addr accepts TCP.
func waitForwarderReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("forwarder did not start in time (last error: %v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
