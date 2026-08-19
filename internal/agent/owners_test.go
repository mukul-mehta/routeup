package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/route"
)

func TestRouteOwnerControlStopsCooperatively(t *testing.T) {
	a := newOwnerTestAgent(t)
	server := httptest.NewServer(a.apiHandler())
	defer server.Close()

	eventsURL := server.URL + ipc.PathOwners + "/myapp?owner_pid=42"
	response, err := http.Get(eventsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	if event := readOwnerEvent(t, reader); event != "ready" {
		t.Fatalf("first event = %q, want ready", event)
	}

	stopResponse := make(chan *http.Response, 1)
	stopErr := make(chan error, 1)
	go func() {
		response, postErr := http.Post(server.URL+ipc.PathOwners+"/myapp/stop", "application/json", nil)
		stopResponse <- response
		stopErr <- postErr
	}()
	if event := readOwnerEvent(t, reader); event != "stop" {
		t.Fatalf("second event = %q, want stop", event)
	}
	ackResponse, err := http.Post(server.URL+ipc.PathOwners+"/myapp/ack?owner_pid=42", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ackResponse.Body.Close()
	if ackResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("ack status = %d, want 204", ackResponse.StatusCode)
	}
	if !a.reg.Unregister("myapp", 42) {
		t.Fatal("test owner did not unregister after acknowledging stop")
	}
	if err := <-stopErr; err != nil {
		t.Fatal(err)
	}
	stopHTTPResponse := <-stopResponse
	_ = stopHTTPResponse.Body.Close()
	if stopHTTPResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("stop status = %d, want 202", stopHTTPResponse.StatusCode)
	}
}

func TestStopOwnerFailsClosedWithoutControlStream(t *testing.T) {
	a := newOwnerTestAgent(t)
	request := httptest.NewRequest(http.MethodPost, ipc.PathOwners+"/myapp/stop", nil)
	response := httptest.NewRecorder()
	a.apiHandler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
	var body ipc.ErrorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OwnerPID != 42 || !strings.Contains(body.Error, "cannot be stopped remotely") {
		t.Fatalf("error body = %#v", body)
	}
}

func TestStopOwnerAcceptsIndependentOwnerCleanup(t *testing.T) {
	a := newOwnerTestAgent(t)
	server := httptest.NewServer(a.apiHandler())
	defer server.Close()
	response, err := http.Get(server.URL + ipc.PathOwners + "/myapp?owner_pid=42")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	if event := readOwnerEvent(t, reader); event != "ready" {
		t.Fatalf("first event = %q, want ready", event)
	}

	stopResponse := make(chan *http.Response, 1)
	go func() {
		result, _ := http.Post(server.URL+ipc.PathOwners+"/myapp/stop", "application/json", nil)
		stopResponse <- result
	}()
	if event := readOwnerEvent(t, reader); event != "stop" {
		t.Fatalf("second event = %q, want stop", event)
	}
	if !a.reg.Unregister("myapp", 42) {
		t.Fatal("test owner did not unregister")
	}
	result := <-stopResponse
	defer func() { _ = result.Body.Close() }()
	if result.StatusCode != http.StatusAccepted {
		t.Fatalf("stop status = %d, want 202", result.StatusCode)
	}
}

func TestOwnerEventsRequireMatchingClaimOwner(t *testing.T) {
	a := newOwnerTestAgent(t)
	request := httptest.NewRequest(http.MethodGet, ipc.PathOwners+"/myapp?owner_pid=7", nil)
	response := httptest.NewRecorder()
	a.apiHandler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
}

func newOwnerTestAgent(t *testing.T) *Agent {
	t.Helper()
	registry := NewRegistry()
	if _, err := registry.Register(ipc.Claim{
		Name: "myapp", Targets: []route.Target{{Path: "/", Port: 8080}}, OwnerPID: 42,
	}); err != nil {
		t.Fatal(err)
	}
	return &Agent{
		reg: registry, owners: newRouteOwnerControls(), logStore: logs.NewStore(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func readOwnerEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "event:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
}
