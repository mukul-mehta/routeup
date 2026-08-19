package agentctl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// WatchRouteOwner keeps a cooperative control stream open for a route owner.
// onReady runs after the agent has attached the stream. stopped is true only
// when another CLI explicitly requested that this owner stop.
func (c *Client) WatchRouteOwner(ctx context.Context, name string, ownerPID int, onReady func()) (stopped bool, err error) {
	query := url.Values{"owner_pid": {fmt.Sprintf("%d", ownerPID)}}
	path := ipc.PathOwners + "/" + url.PathEscape(name) + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return false, fmt.Errorf("build route owner request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, decodeErrorResponse(resp)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return false, fmt.Errorf("agent returned content type %q for route owner stream", resp.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if readErr == io.EOF {
				readErr = io.ErrUnexpectedEOF
			}
			return false, fmt.Errorf("read route owner stream: %w", readErr)
		}
		event := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		if !strings.HasPrefix(line, "event:") {
			continue
		}
		switch event {
		case "ready":
			if onReady != nil {
				onReady()
			}
		case "stop":
			if err := c.acknowledgeRouteStop(ctx, name, ownerPID); err != nil {
				return false, err
			}
			return true, nil
		}
	}
}

func (c *Client) acknowledgeRouteStop(ctx context.Context, name string, ownerPID int) error {
	query := url.Values{"owner_pid": {fmt.Sprintf("%d", ownerPID)}}
	path := ipc.PathOwners + "/" + url.PathEscape(name) + "/ack?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, nil)
	if err != nil {
		return fmt.Errorf("build route stop acknowledgment: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("acknowledge route stop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return decodeErrorResponse(resp)
	}
	return nil
}

// StopRoute asks the live route owner to exit. It never sends an OS signal.
// found is false when the route is not active.
func (c *Client) StopRoute(ctx context.Context, name string) (found bool, err error) {
	path := ipc.PathOwners + "/" + url.PathEscape(name) + "/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, nil)
	if err != nil {
		return false, fmt.Errorf("build stop route request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, nil
	}
	if resp.StatusCode != http.StatusAccepted {
		return false, decodeErrorResponse(resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return true, nil
}
