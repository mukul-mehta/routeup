// Package agentctl is the CLI side of the routeup agent IPC, the counterpart to
// internal/agent. That package is the daemon; this one is the stub the CLI uses
// to talk to it, as JSON over HTTP/1.1 across the per-user Unix socket, using
// the shared types in internal/ipc.
//
// This file is the request/response surface (the /v1 routes). The process
// lifecycle the client drives (EnsureRunning, Stop, Restart, spawn, staleness)
// lives in lifecycle.go.
package agentctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// Client is the CLI side of the agent IPC. It speaks JSON over HTTP/1.1 over
// the per-user Unix domain socket.
type Client struct {
	socketPath string
	httpClient *http.Client
	execPath   string
	version    string
}

// NewClient returns a client that dials socketPath for all requests.
//
//	execPath is the binary to re-exec when spawning an agent; pass "" to use
//	         os.Executable().
//	version  is the CLI's own build version, used to detect a stale running
//	         agent. Pass "" to disable the version half of staleness checks.
//
// The HTTP client sets no timeout of its own: every call is bounded by the
// context passed to it, so callers control deadlines per request (see
// timeouts.go).
func NewClient(socketPath, execPath, version string) *Client {
	return &Client{
		socketPath: socketPath,
		execPath:   execPath,
		version:    version,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// Status fetches /v1/status. A non-nil error usually means the agent is not
// running or the socket is unreachable.
func (c *Client) Status(ctx context.Context) (ipc.Status, error) {
	var s ipc.Status
	if err := c.get(ctx, ipc.PathStatus, &s); err != nil {
		return ipc.Status{}, err
	}
	return s, nil
}

// Register sends a POST /v1/routes for claim. The OwnerPID/CWD must be
// populated by the caller. On 409, the returned error is a
// *ipc.ConflictError whose Existing field describes the holding claim.
func (c *Client) Register(ctx context.Context, claim ipc.Claim) (ipc.Claim, error) {
	body, err := json.Marshal(claim)
	if err != nil {
		return ipc.Claim{}, fmt.Errorf("encode claim: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://unix"+ipc.PathRoutes, bytes.NewReader(body))
	if err != nil {
		return ipc.Claim{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ipc.Claim{}, fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusConflict {
		var e ipc.ErrorBody
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return ipc.Claim{}, &ipc.ConflictError{
			Name: claim.Name,
			Existing: ipc.Claim{
				Name:     claim.Name,
				OwnerPID: e.OwnerPID,
				OwnerCWD: e.OwnerCWD,
			},
		}
	}
	if resp.StatusCode != http.StatusCreated {
		return ipc.Claim{}, decodeErrorResponse(resp)
	}

	var out ipc.Claim
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ipc.Claim{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// Unregister sends DELETE /v1/routes/{name}. It is idempotent.
func (c *Client) Unregister(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		"http://unix"+ipc.PathRoutes+"/"+url.PathEscape(name), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		return decodeErrorResponse(resp)
	}
	return nil
}

// List sends GET /v1/routes.
func (c *Client) List(ctx context.Context) ([]ipc.Claim, error) {
	var wrapper struct {
		Routes []ipc.Claim `json:"routes"`
	}
	if err := c.get(ctx, ipc.PathRoutes, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Routes, nil
}

// get performs a GET against the agent and decodes a JSON body into out (pass
// nil to discard the body).
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return decodeErrorResponse(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// decodeErrorResponse turns a non-2xx response into an error, preferring the
// JSON error body's message when present.
func decodeErrorResponse(resp *http.Response) error {
	var e ipc.ErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return fmt.Errorf("agent returned %s", resp.Status)
	}
	if e.Error == "" {
		return errors.New(resp.Status)
	}
	return fmt.Errorf("agent: %s", e.Error)
}
