package agentctl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestClientWatchesAndStopsRouteOwner(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "agent.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	ack := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/owners/myapp":
			if r.URL.Query().Get("owner_pid") != "42" {
				http.Error(w, "wrong owner", http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			<-stop
			_, _ = fmt.Fprint(w, "event: stop\ndata: {}\n\n")
			w.(http.Flusher).Flush()
		case r.Method == http.MethodPost && r.URL.Path == "/v1/owners/myapp/stop":
			close(stop)
			<-ack
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/owners/myapp/ack":
			close(ack)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()

	client := NewClient(socketPath, "", "")
	ready := make(chan struct{})
	watchDone := make(chan bool, 1)
	go func() {
		stopped, watchErr := client.WatchRouteOwner(context.Background(), "myapp", 42, func() { close(ready) })
		if watchErr != nil {
			t.Errorf("WatchRouteOwner: %v", watchErr)
		}
		watchDone <- stopped
	}()
	<-ready
	found, err := client.StopRoute(context.Background(), "myapp")
	if err != nil || !found {
		t.Fatalf("StopRoute = %v, %v", found, err)
	}
	if stopped := <-watchDone; !stopped {
		t.Fatal("owner stream ended without stop event")
	}
}

func TestStopRouteClassifiesUnavailableOwnerControl(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "agent.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/owners/myapp/stop" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		http.NotFound(w, r)
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()

	_, err = NewClient(socketPath, "", "").StopRoute(context.Background(), "myapp")
	if !errors.Is(err, ErrRouteOwnerControlUnavailable) {
		t.Fatalf("StopRoute error = %v, want ErrRouteOwnerControlUnavailable", err)
	}
}
