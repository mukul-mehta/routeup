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
		Name:          "myapp",
		Targets:       []route.Target{{Path: "/", Port: 8080}},
		CaptureRequest: true,
		RedactHeaders:  []string{"Authorization"},
		OwnerPID:      os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}

	targets, captureReq, _, redactHeaders, ok := registry.LookupTargets("myapp")
	if !ok || !captureReq || len(targets) != 1 || targets[0] != (route.Target{Path: "/", Port: 8080}) || len(redactHeaders) != 1 || redactHeaders[0] != "Authorization" {
		t.Fatalf("LookupTargets() = %#v, %t, %#v, %t", targets, captureReq, redactHeaders, ok)
	}
}
