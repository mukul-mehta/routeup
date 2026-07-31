package process

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunner_ExitCode(t *testing.T) {
	code, err := Runner{Command: "exit 7"}.Run(context.Background(), Stdio{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestRunner_EmptyCommand(t *testing.T) {
	if _, err := (Runner{Command: "  "}).Run(context.Background(), Stdio{}); err == nil {
		t.Fatal("expected error for empty command, got nil")
	}
}

func TestEnsurePortAvailable_Occupied(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := EnsurePortAvailable(port); err == nil {
		t.Fatalf("expected occupied port %d to fail", port)
	}
}

func TestRunner_CancelTerminatesProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Runner{
			Command:   `trap '' TERM; sleep 30 & echo $! > "$CHILD_PID_FILE"; wait`,
			Env:       append(os.Environ(), "CHILD_PID_FILE="+pidFile),
			KillGrace: 100 * time.Millisecond,
		}.Run(ctx, Stdio{})
		done <- err
	}()

	pid := waitForPIDFile(t, pidFile)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop after cancellation")
	}

	deadline := time.Now().Add(time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("descendant process %d survived cancellation", pid)
	}
}

func TestRunner_ExitTerminatesProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	code, err := Runner{
		Command:   `sleep 30 & echo $! > "$CHILD_PID_FILE"`,
		Env:       append(os.Environ(), "CHILD_PID_FILE="+pidFile),
		KillGrace: 100 * time.Millisecond,
	}.Run(context.Background(), Stdio{})
	if err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	pid := waitForPIDFile(t, pidFile)
	if processExists(pid) {
		t.Fatalf("descendant process %d survived shell exit", pid)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr != nil {
				t.Fatalf("parse child pid: %v", convErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read child pid: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for child pid")
	return 0
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
