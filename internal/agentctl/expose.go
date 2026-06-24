package agentctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// Expose sends POST /v1/expose and returns the granted public host.
// Establishing a tunnel (dial + claim round trip) is slower than an ordinary
// control call, so when the caller passes a context with no deadline we apply
// tunnelHandshakeTimeout; a caller-set deadline always wins.
func (c *Client) Expose(ctx context.Context, req ipc.ExposeRequest) (ipc.ExposeResponse, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, tunnelHandshakeTimeout)
		defer cancel()
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ipc.ExposeResponse{}, fmt.Errorf("encode expose: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://unix"+ipc.PathExpose, bytes.NewReader(body))
	if err != nil {
		return ipc.ExposeResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ipc.ExposeResponse{}, fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ipc.ExposeResponse{}, decodeErrorResponse(resp)
	}
	var out ipc.ExposeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ipc.ExposeResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// Unexpose sends POST /v1/unexpose for host. It is idempotent.
func (c *Client) Unexpose(ctx context.Context, host string) error {
	body, err := json.Marshal(ipc.UnexposeRequest{Host: host})
	if err != nil {
		return fmt.Errorf("encode unexpose: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://unix"+ipc.PathUnexpose, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

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
