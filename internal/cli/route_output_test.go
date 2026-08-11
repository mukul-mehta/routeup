package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/route"
)

func TestWriteRouteReadyEvent(t *testing.T) {
	var output bytes.Buffer
	err := writeRouteReadyEvent(&output, routeReadyEvent{
		Route: "myapp", LocalURL: "https://myapp.localhost",
		PublicURL: "https://myapp.example", Targets: []route.Target{{Path: "/", Port: 8080}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var event routeReadyEvent
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "ready" || event.Route != "myapp" || event.PublicURL != "https://myapp.example" {
		t.Fatalf("event = %#v", event)
	}
}

func TestWriteRouteQRIncludesURL(t *testing.T) {
	var output bytes.Buffer
	writeRouteQR(&output, "https://myapp.example")
	if !strings.Contains(output.String(), "https://myapp.example") || output.Len() < 100 {
		t.Fatalf("QR output = %q", output.String())
	}
}

func TestJSONAndQRAreMutuallyExclusive(t *testing.T) {
	for _, command := range []*cobra.Command{newServeCmd(), newExposeCmd()} {
		command.SetArgs([]string{"--json", "--qr"})
		if err := command.Execute(); err == nil {
			t.Fatalf("%s accepted --json with --qr", command.Name())
		}
	}
}
