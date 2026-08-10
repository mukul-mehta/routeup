// Request-log calls are split from client.go because follow keeps an IPC HTTP
// response open. Finite Logs uses the normal JSON request/response pattern;
// FollowLogs reads the agent's SSE records until its context is cancelled.
//
//	routeup logs -> Client.FollowLogs -> GET /v1/logs?follow=true -> SSE
package agentctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
)

// Logs returns the agent's current metadata-only request-log snapshot.
func (c *Client) Logs(ctx context.Context, opts logs.ListOptions) ([]logs.Entry, error) {
	var wrapper struct {
		Logs []logs.Entry `json:"logs"`
	}
	if err := c.get(ctx, logPath(opts, false), &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Logs, nil
}

// FollowLogs calls handle once for each existing and future matching entry. It
// blocks until ctx is cancelled, the agent closes the stream, or handle fails.
func (c *Client) FollowLogs(ctx context.Context, opts logs.ListOptions, handle func(logs.Entry) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+logPath(opts, true), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return decodeErrorResponse(resp)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("agent returned content type %q for request log stream", resp.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("read request log stream: %w", io.ErrUnexpectedEOF)
			}
			return fmt.Errorf("read request log stream: %w", err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var entry logs.Entry
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &entry); err != nil {
			return fmt.Errorf("decode request log event: %w", err)
		}
		if err := handle(entry); err != nil {
			return fmt.Errorf("handle request log event: %w", err)
		}
	}
}

func logPath(opts logs.ListOptions, follow bool) string {
	query := url.Values{}
	if opts.Route != "" {
		query.Set("route", opts.Route)
	}
	if opts.Source != "" {
		query.Set("source", string(opts.Source))
	}
	if opts.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	if !opts.Since.IsZero() {
		query.Set("since", opts.Since.Format(time.RFC3339Nano))
	}
	if opts.Method != "" {
		query.Set("method", opts.Method)
	}
	if opts.Status != 0 {
		query.Set("status", fmt.Sprintf("%d", opts.Status))
	}
	if follow {
		query.Set("follow", "true")
	}
	if encoded := query.Encode(); encoded != "" {
		return ipc.PathLogs + "?" + encoded
	}
	return ipc.PathLogs
}
