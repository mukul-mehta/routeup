package agent

import (
	"os"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
)

func TestRegistryLookupTargetsIncludesCaptureSetting(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.Register(ipc.Claim{
		Name:     "myapp",
		Targets:  []route.Target{{Path: "/", Port: 8080}},
		Capture:  true,
		OwnerPID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}

	targets, capture, ok := registry.LookupTargets("myapp")
	if !ok || !capture || len(targets) != 1 || targets[0] != (route.Target{Path: "/", Port: 8080}) {
		t.Fatalf("LookupTargets() = %#v, %t, %t", targets, capture, ok)
	}
}
