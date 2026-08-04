package agentctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/logs"
)

func TestClientLogsAndFollowLogs(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}

	entry := logs.Entry{
		ID:     "req_one",
		Source: logs.SourcePublic,
		Route:  "api.myapp",
		Capture: &logs.Capture{Request: logs.CapturedMessage{
			Body:     []byte("payload"),
			Complete: true,
		}},
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/requests/req_one" {
			_ = json.NewEncoder(w).Encode(entry)
			return
		}
		if r.URL.Query().Get("route") != "api.myapp" || r.URL.Query().Get("source") != "public" {
			http.Error(w, "unexpected query", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("follow") != "true" {
			_ = json.NewEncoder(w).Encode(map[string][]logs.Entry{"logs": {entry}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		body, err := json.Marshal(entry)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	go func() { _ = server.Serve(listener) }()

	client := NewClient(socketPath, "", "")
	entries, err := client.Logs(context.Background(), logs.ListOptions{Route: "api.myapp", Source: logs.SourcePublic})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("entries = %#v, want req_one", entries)
	}
	inspected, err := client.Inspect(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Capture == nil || string(inspected.Capture.Request.Body) != "payload" {
		t.Fatalf("Inspect() = %#v, want retained payload", inspected)
	}

	stop := errors.New("stop after first event")
	err = client.FollowLogs(context.Background(), logs.ListOptions{Route: "api.myapp", Source: logs.SourcePublic}, func(got logs.Entry) error {
		if got.ID != entry.ID {
			t.Fatalf("follow entry = %#v, want req_one", got)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("FollowLogs() error = %v, want stop", err)
	}
}
