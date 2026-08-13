package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mdp/qrterminal/v3"

	"github.com/mukul-mehta/routeup/internal/route"
)

type routeReadyEvent struct {
	Event         string         `json:"event"`
	Route         string         `json:"route"`
	LocalURL      string         `json:"local_url,omitempty"`
	PublicURL     string         `json:"public_url,omitempty"`
	ExposurePaths []string       `json:"exposure_paths,omitempty"`
	Targets       []route.Target `json:"targets"`
}

func writeRouteReadyEvent(out io.Writer, event routeReadyEvent) error {
	event.Event = "ready"
	if err := json.NewEncoder(out).Encode(event); err != nil {
		return fmt.Errorf("write ready event: %w", err)
	}
	return nil
}

func writeRouteQR(out io.Writer, url string) {
	_, _ = fmt.Fprintf(out, "\n%s\n", newTerminalStyles(out).url(url))
	qrterminal.GenerateHalfBlock(url, qrterminal.L, out)
}
