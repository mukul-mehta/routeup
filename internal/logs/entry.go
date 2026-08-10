// Package logs defines the agent-local request log and bounded request store.
//
// A future proxy creates one Entry after it finishes handling an HTTP request.
// Store retains the entry for `routeup logs`, while Capture, defined in
// capture.go, holds an optional original request for inspect.
//
// An entry carries two intentionally different paths:
//
//	RequestPath  the incoming URL path, such as /api/webhooks/github
//	Target.Path   the configured target prefix selected for that request, such as /api
//
// The target uses route.Target rather than a duplicate logs type so the proxy,
// registry, and log all share the same path/port representation.
//
//	proxy --> Entry --> Store --> agent API --> CLI
package logs

import (
	"time"

	"github.com/mukul-mehta/routeup/internal/route"
)

// Source identifies how a request reached the local agent.
type Source string

const (
	// SourceLocal is traffic received at a .localhost route.
	SourceLocal Source = "local"

	// SourcePublic is traffic received through a public tunnel.
	SourcePublic Source = "public"
)

// Entry is one completed request handled by the local agent. Captured remains
// available in metadata-only lists; Capture is present only in inspect results.
type Entry struct {
	ID          string        `json:"id"`
	StartedAt   time.Time     `json:"started_at"`
	Duration    time.Duration `json:"duration"`
	Source      Source        `json:"source"`
	Route       string        `json:"route"`
	Host        string        `json:"host"`
	Method      string        `json:"method"`
	RequestPath string        `json:"request_path"`
	Target      route.Target  `json:"target"`
	Status      int           `json:"status"`
	Captured    bool          `json:"captured,omitempty"`
	Capture     *Capture      `json:"capture,omitempty"`
}

func (source Source) valid() bool {
	return source == SourceLocal || source == SourcePublic
}
