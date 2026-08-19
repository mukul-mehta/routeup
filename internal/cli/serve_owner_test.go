package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/logs"
)

func TestFollowServeLogsFiltersFromRegistrationAndPrintsEntries(t *testing.T) {
	since := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	querySeen := make(chan struct{}, 1)
	socketPath := startUnixHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("route") != "myapp" || r.URL.Query().Get("since") != since.Format(time.RFC3339Nano) {
			http.Error(w, "unexpected filters", http.StatusBadRequest)
			return
		}
		querySeen <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		entry := logs.Entry{
			ID: "req_1234567890abcdef", Route: "myapp", Source: logs.SourceLocal,
			Method: http.MethodGet, RequestPath: "/health", Status: http.StatusOK, StartedAt: since.Add(time.Second),
		}
		body, _ := json.Marshal(entry)
		_, _ = w.Write([]byte("id: " + entry.ID + "\ndata: " + string(body) + "\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	client := agentctl.NewClient(socketPath, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &matchingWriter{match: "req_1234567890abcdef", matched: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() {
		done <- followServeLogs(ctx, client, logs.ListOptions{Route: "myapp", Since: since}, writer, writer)
	}()
	select {
	case <-querySeen:
	case <-t.Context().Done():
		t.Fatal("serve log stream did not connect")
	}
	select {
	case <-writer.matched:
		cancel()
	case <-t.Context().Done():
		cancel()
		t.Fatal("serve log stream did not print request")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type matchingWriter struct {
	match   string
	matched chan struct{}
}

func (w *matchingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.match) {
		select {
		case w.matched <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}
