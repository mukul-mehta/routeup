package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
)

func TestTunnelManagerRemovesFinishedSession(t *testing.T) {
	manager := newTunnelManager(context.Background(), logs.NewStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, cancel := context.WithCancel(context.Background())
	session := &tunnelSession{
		host: "api.example", route: "api.myapp", ownerPID: 42,
		state: ipc.ExposureConnected, cancel: cancel,
	}
	if err := manager.store(session.host, session); err != nil {
		t.Fatal(err)
	}

	manager.finish(session, errors.New("permanent failure"))
	if statuses := manager.statuses(); len(statuses) != 0 {
		t.Fatalf("statuses = %#v, want terminally failed tunnel removed", statuses)
	}
}

func TestTunnelManagerRejectsHostReplacement(t *testing.T) {
	manager := newTunnelManager(context.Background(), logs.NewStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, cancelOld := context.WithCancel(context.Background())
	old := &tunnelSession{host: "api.example", route: "api.myapp", ownerPID: 42, state: ipc.ExposureConnected, cancel: cancelOld}
	if err := manager.store(old.host, old); err != nil {
		t.Fatal(err)
	}
	_, cancelNew := context.WithCancel(context.Background())
	replacement := &tunnelSession{host: old.host, route: old.route, ownerPID: old.ownerPID, state: ipc.ExposureConnected, cancel: cancelNew}
	if err := manager.store(replacement.host, replacement); err == nil {
		t.Fatal("replacement unexpectedly displaced active host")
	}

	statuses := manager.statuses()
	if len(statuses) != 1 || statuses[0].Host != old.host {
		t.Fatalf("statuses = %#v, want original retained", statuses)
	}
}

func TestTunnelManagerRejectsConcurrentPublicClaim(t *testing.T) {
	manager := newTunnelManager(context.Background(), logs.NewStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, cancelFirst := context.WithCancel(context.Background())
	claim := exposureClaim{server: "https://edge.example", name: "api-myapp"}
	first := &tunnelSession{route: "api.myapp", ownerPID: 42, cancel: cancelFirst, claim: claim}
	if err := manager.reserve(first); err != nil {
		t.Fatal(err)
	}
	_, cancelSecond := context.WithCancel(context.Background())
	second := &tunnelSession{route: "other", ownerPID: 7, cancel: cancelSecond, claim: claim}
	if err := manager.reserve(second); err == nil {
		t.Fatal("second owner reserved the same public claim")
	}
}

func TestTunnelManagerRejectsChangedHostOnReconnect(t *testing.T) {
	manager := newTunnelManager(context.Background(), logs.NewStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, cancel := context.WithCancel(context.Background())
	session := &tunnelSession{host: "old.example", route: "api", ownerPID: 42, cancel: cancel, state: ipc.ExposureConnected}
	if err := manager.store(session.host, session); err != nil {
		t.Fatal(err)
	}
	if manager.setSessionState(session, "new.example", ipc.ExposureConnected) {
		t.Fatal("changed reconnect host accepted")
	}
	if session.host != "old.example" {
		t.Fatalf("session host = %q, want old.example", session.host)
	}
}

func TestTunnelManagerUnexposeRequiresOwner(t *testing.T) {
	manager := newTunnelManager(context.Background(), logs.NewStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, cancel := context.WithCancel(context.Background())
	session := &tunnelSession{host: "api.example", route: "api.myapp", ownerPID: 42, state: ipc.ExposureConnected, cancel: cancel}
	if err := manager.store(session.host, session); err != nil {
		t.Fatal(err)
	}

	if manager.Unexpose(ipc.UnexposeRequest{Host: session.host, Route: session.route, OwnerPID: 7}) {
		t.Fatal("different owner removed tunnel")
	}
	if !manager.Unexpose(ipc.UnexposeRequest{Host: session.host, Route: session.route, OwnerPID: session.ownerPID}) {
		t.Fatal("owning process did not remove tunnel")
	}
}
