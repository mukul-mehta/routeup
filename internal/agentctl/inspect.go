// Inspect retrieves one retained exchange from the agent. It uses a finite JSON
// response, unlike the open-ended SSE connection used by FollowLogs.
package agentctl

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/logs"
)

// Inspect returns one log entry with its retained exchange data.
func (c *Client) Inspect(ctx context.Context, id string) (logs.Entry, error) {
	if !validRequestID(id) {
		return logs.Entry{}, errors.New("invalid request id (want req_ followed by 16 base64url characters)")
	}
	var entry logs.Entry
	if err := c.get(ctx, ipc.PathRequests+"/"+url.PathEscape(id), &entry); err != nil {
		return logs.Entry{}, fmt.Errorf("inspect request %s: %w", id, err)
	}
	return entry, nil
}

func validRequestID(id string) bool {
	if len(id) != len("req_")+16 || !strings.HasPrefix(id, "req_") {
		return false
	}
	for _, r := range id[len("req_"):] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
