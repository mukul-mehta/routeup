// Package tunnel implements the routeup request tunnel: a WebSocket connection
// from the local agent to the public server, multiplexed with yamux so each
// public HTTP request becomes one stream.
//
// Roles: the agent dials and is the yamux client; the public server accepts and
// is the yamux server. The agent opens a single control stream to assert its
// route claims; the server opens one stream per inbound public request, which
// the agent serves against its local handler.
//
// This file holds the wire protocol shared by both ends. server.go is the
// server-side hub; client.go is the agent-side client.
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/hashicorp/yamux"
)

const (
	Version       = "routeup-tunnel/1"
	VersionHeader = "Routeup-Tunnel-Version"
	Path          = "/_routeup/tunnel"
)

const (
	msgClaim    = "claim"
	msgClaimOK  = "claim_ok"
	msgClaimErr = "claim_err"
)

type ControlMsg struct {
	Type    string      `json:"type"`
	Claims  []ClaimSpec `json:"claims,omitempty"`
	Granted []string    `json:"granted,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    int         `json:"code,omitempty"`
}

type ClaimSpec struct {
	Route  string `json:"route"`
	Random bool   `json:"random"`
}

type ClaimKeeper interface {
	Hold(ctx context.Context, token string, spec ClaimSpec) (host string, err error)
	Release(host string)
}

type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

func yamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	return c
}

func writeControl(w io.Writer, msg ControlMsg) error {
	return json.NewEncoder(w).Encode(msg)
}

func statusCodeOf(err error) int {
	var c interface{ StatusCode() int }
	if errors.As(err, &c) {
		return c.StatusCode()
	}
	return 0
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(header, prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
