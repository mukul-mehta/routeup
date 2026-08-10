package agentctl

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
)

func TestReconcileDesiredRestoresClaimAndExposure(t *testing.T) {
	var registerCalls atomic.Int32
	var exposeCalls atomic.Int32
	claim := ipc.Claim{Name: "api.myapp", Targets: []route.Target{{Path: "/", Port: 8080}}, OwnerPID: 42}
	exposure := ipc.ExposeRequest{
		Name: "api-myapp", Route: claim.Name, Targets: claim.Targets,
		Server: "https://edge.example", OwnerPID: claim.OwnerPID,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+ipc.PathStatus, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ipc.Status{BootID: "new-boot"})
	})
	mux.HandleFunc("POST "+ipc.PathRoutes, func(w http.ResponseWriter, r *http.Request) {
		registerCalls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(claim)
	})
	mux.HandleFunc("POST "+ipc.PathExpose, func(w http.ResponseWriter, _ *http.Request) {
		exposeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(ipc.ExposeResponse{Host: "api-myapp.example"})
	})

	client := newTestUnixClient(t, mux)
	var diagnostics bytes.Buffer
	bootID, exposureFailed := client.reconcileDesired(context.Background(), DesiredState{
		Claim: &claim, Exposure: &exposure, PublicHost: "api-myapp.example",
	}, "old-boot", &diagnostics)
	if exposureFailed || bootID != "new-boot" || registerCalls.Load() != 1 || exposeCalls.Load() != 1 {
		t.Fatalf("boot=%q failed=%v register=%d expose=%d diagnostics=%q", bootID, exposureFailed, registerCalls.Load(), exposeCalls.Load(), diagnostics.String())
	}
}

func TestReconcileDesiredKeepsManagedExposure(t *testing.T) {
	var exposeCalls atomic.Int32
	exposure := ipc.ExposeRequest{Route: "api.myapp", OwnerPID: 42}
	status := ipc.Status{BootID: "same-boot", Exposures: []ipc.ExposureStatus{{
		Route: exposure.Route, Host: "api.example", OwnerPID: exposure.OwnerPID, State: ipc.ExposureReconnecting,
	}}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+ipc.PathStatus, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("POST "+ipc.PathExpose, func(http.ResponseWriter, *http.Request) {
		exposeCalls.Add(1)
	})

	client := newTestUnixClient(t, mux)
	_, exposureFailed := client.reconcileDesired(context.Background(), DesiredState{
		Exposure: &exposure, PublicHost: "api.example",
	}, status.BootID, &bytes.Buffer{})
	if exposureFailed || exposeCalls.Load() != 0 {
		t.Fatalf("expose calls = %d, want no duplicate while reconnecting", exposeCalls.Load())
	}
}

func newTestUnixClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	socketPath := filepath.Join(shortSocketDir(t), "agent.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return NewClient(socketPath, "", "")
}
