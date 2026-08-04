// Inspect retrieves one retained request from the agent. It uses a finite JSON
// response, unlike the open-ended SSE connection used by FollowLogs.
package agentctl

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
)

// Inspect returns one log entry with its retained request data.
func (c *Client) Inspect(ctx context.Context, id string) (logs.Entry, error) {
	if id == "" {
		return logs.Entry{}, errors.New("request id is required")
	}
	var entry logs.Entry
	if err := c.get(ctx, ipc.PathRequests+"/"+url.PathEscape(id), &entry); err != nil {
		return logs.Entry{}, fmt.Errorf("inspect request %s: %w", id, err)
	}
	return entry, nil
}
