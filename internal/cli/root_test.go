package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/state"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv(state.StateDirEnv)
	os.Exit(m.Run())
}

func startUnixHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return socketPath
}

func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)

	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestRoot_Version(t *testing.T) {
	stdout, _, err := runRoot(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}

	const want = "0.0.0-dev"
	if !strings.Contains(stdout, want) {
		t.Errorf("--version output missing %q\n--- got ---\n%s", want, stdout)
	}
}

func TestRoot_HelpGroupsCommandsByWorkflow(t *testing.T) {
	stdout, _, err := runRoot(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"Start:", "Observe:", "Manage:"} {
		if !strings.Contains(stdout, heading) {
			t.Fatalf("help output missing %q:\n%s", heading, stdout)
		}
	}
}

func TestLogs_NoAgentMessage(t *testing.T) {
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	stdout, stderr, err := runRoot(t, "logs")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	const want = "no request logs (agent not running)"
	if !strings.Contains(stdout, want) {
		t.Errorf("logs output missing %q\n--- got ---\n%s", want, stdout)
	}
}

func TestLogsFollowDoesNotPrintHeaderWithoutAgent(t *testing.T) {
	dir, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(dir, "missing.sock"))
	stdout, _, err := runRoot(t, "logs", "--follow")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "TIME") || !strings.Contains(stdout, "agent not running") {
		t.Fatalf("output = %q", stdout)
	}
}

func TestLogsRejectsConflictingSourceFlags(t *testing.T) {
	_, _, err := runRoot(t, "logs", "--public", "--local")
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("logs conflict error = %v", err)
	}
}

func TestCommandsRejectNameWithRandom(t *testing.T) {
	for _, args := range [][]string{{"serve", "api", "--random"}, {"expose", "api", "--random"}} {
		_, _, err := runRoot(t, args...)
		if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
			t.Fatalf("%v error = %v, want conflict", args, err)
		}
	}
}

func TestLogsJSONKeepsStdoutMachineReadable(t *testing.T) {
	t.Run("missing agent", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "rup-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(dir, "missing.sock"))
		stdout, _, err := runRoot(t, "logs", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("no records", func(t *testing.T) {
		socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": []any{}})
		}))
		t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
		stdout, _, err := runRoot(t, "logs", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("agent error", func(t *testing.T) {
		socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "broken", http.StatusInternalServerError)
		}))
		t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
		stdout, _, err := runRoot(t, "logs", "--json")
		if err == nil {
			t.Fatal("expected agent error")
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})
}

func TestLogsSendsFiltersToAgent(t *testing.T) {
	queryCh := make(chan map[string]string, 1)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := make(map[string]string)
		for _, key := range []string{"method", "status", "limit", "since"} {
			query[key] = r.URL.Query().Get(key)
		}
		queryCh <- query
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": []any{}})
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	stdout, _, err := runRoot(t, "logs", "--json", "--method", "post", "--status", "202", "--limit", "10", "--since", "5m")
	if err != nil || stdout != "" {
		t.Fatalf("stdout=%q error=%v", stdout, err)
	}
	query := <-queryCh
	if query["method"] != "POST" || query["status"] != "202" || query["limit"] != "10" || query["since"] == "" {
		t.Fatalf("query = %#v", query)
	}
}

func TestMachineReadableStatusCommandsWithoutAgent(t *testing.T) {
	dir, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(dir, "missing.sock"))

	routesOut, _, err := runRoot(t, "routes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(routesOut) != "[]" {
		t.Fatalf("routes json = %q, want []", routesOut)
	}

	statusOut, _, err := runRoot(t, "agent", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]bool
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil || status["running"] {
		t.Fatalf("agent status json = %q, error = %v", statusOut, err)
	}
}

func TestInspectJSONIncludesCapturedBytes(t *testing.T) {
	entry := logs.Entry{
		ID: "req_1234567890abcdef", Source: logs.SourcePublic, Captured: true,
		Capture: &logs.Capture{Request: &logs.CapturedMessage{Body: []byte{0, 0x1b, 0xff}, Complete: true}},
	}
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ipc.PathRequests+"/"+entry.ID {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(entry)
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	stdout, _, err := runRoot(t, "inspect", entry.ID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded logs.Entry
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Capture == nil || !bytes.Equal(decoded.Capture.Request.Body, entry.Capture.Request.Body) {
		t.Fatalf("inspect json = %#v, want body %v", decoded, entry.Capture.Request.Body)
	}
}

func TestInspectRejectsUnsafeRequestID(t *testing.T) {
	_, _, err := runRoot(t, "inspect", "req_bad\x1b[2J")
	if err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("error = %q, want safe invalid-id error", err)
	}
}

func TestInspectReportsUnavailableAgentClearly(t *testing.T) {
	dir, err := os.MkdirTemp("", "rup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("ROUTEUP_AGENT_SOCKET", filepath.Join(dir, "missing.sock"))
	_, _, err = runRoot(t, "inspect", "req_1234567890abcdef")
	if err == nil || err.Error() != "agent not running; retained requests are unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestAgentStatusShowsStandaloneExposures(t *testing.T) {
	status := ipc.Status{BootID: "boot", Exposures: []ipc.ExposureStatus{{
		Route: "myapp", Host: "myapp.try.routeup.dev", Paths: []string{"/api/*"}, OwnerPID: 42, State: ipc.ExposureConnected,
	}}}
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ipc.PathStatus {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(status)
	}))
	t.Setenv("ROUTEUP_AGENT_SOCKET", socketPath)
	stdout, _, err := runRoot(t, "agent", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"public exposures:", "connected", "myapp", "https://myapp.try.routeup.dev", "/api/*", "pid 42"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status output %q missing %q", stdout, want)
		}
	}
}
