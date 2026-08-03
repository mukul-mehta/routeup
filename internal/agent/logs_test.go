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

	"github.com/mukul-mehta/routeup/internal/logs"
)

func TestHandleLogsFiltersMetadata(t *testing.T) {
	a := &Agent{logStore: logs.NewStore(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, err := a.logStore.Append(logs.Entry{ID: "req_local", Source: logs.SourceLocal, Route: "myapp"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.logStore.Append(logs.Entry{ID: "req_public", Source: logs.SourcePublic, Route: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/logs?route=myapp&source=local", nil)
	response := httptest.NewRecorder()
	a.apiHandler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	var body struct {
		Logs []logs.Entry `json:"logs"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Logs) != 1 || body.Logs[0].ID != "req_local" || body.Logs[0].Capture != nil {
		t.Fatalf("logs = %#v, want metadata-only req_local", body.Logs)
	}
}

func TestHandleLogsFollowStreamsExistingAndNewEntries(t *testing.T) {
	a := &Agent{logStore: logs.NewStore(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, err := a.logStore.Append(logs.Entry{ID: "req_existing", Source: logs.SourcePublic, Route: "myapp"})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(a.apiHandler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/logs?follow=true&source=public")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", contentType)
	}

	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("event line = %q", line)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	var existing logs.Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &existing); err != nil {
		t.Fatal(err)
	}
	if existing.ID != "req_existing" {
		t.Fatalf("initial event = %#v, want req_existing", existing)
	}
	_, err = a.logStore.Append(logs.Entry{ID: "req_new", Source: logs.SourcePublic, Route: "myapp"})
	if err != nil {
		t.Fatal(err)
	}
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("event line = %q", line)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	var newEntry logs.Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &newEntry); err != nil {
		t.Fatal(err)
	}
	if newEntry.ID != "req_new" {
		t.Fatalf("new event = %#v, want req_new", newEntry)
	}
}

func TestHandleLogsRejectsInvalidSource(t *testing.T) {
	a := &Agent{logStore: logs.NewStore(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodGet, "/v1/logs?source=external", nil)
	response := httptest.NewRecorder()
	a.apiHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
