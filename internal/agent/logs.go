// Request-log API flow:
//
//	proxy -> logs.Store.Append -> GET /v1/logs -> routeup logs
//
// A normal GET returns the store's current metadata-only snapshot as JSON. A
// follow GET converts the store's atomic snapshot-and-change signal into SSE:
// it sends the current records, waits for an append, then reads the bounded ring
// again. The store has no per-follower queues, so a slow CLI cannot block proxy
// requests or accumulate unbounded agent memory.
package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mukul-mehta/routeup/internal/logs"
)

func (a *Agent) handleLogs(w http.ResponseWriter, r *http.Request) {
	opts, follow, err := parseLogOptions(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	if !follow {
		writeJSON(w, http.StatusOK, map[string]any{"logs": a.logStore.List(opts)})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming response is unavailable", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	lastID := ""
	first := true
	for {
		entries, changed := a.logStore.ListAndWatch(opts)
		if first {
			first = false
			opts.Limit = 0
		}
		entries = entriesAfter(entries, lastID)
		for _, entry := range entries {
			body, err := json.Marshal(entry)
			if err != nil {
				a.logger.Error("encode request log event", "err", err)
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
				return
			}
			lastID = entry.ID
		}
		flusher.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}

func parseLogOptions(r *http.Request) (logs.ListOptions, bool, error) {
	query := r.URL.Query()
	opts := logs.ListOptions{Route: query.Get("route"), Method: query.Get("method")}
	source := query.Get("source")
	if source != "" {
		opts.Source = logs.Source(source)
		if opts.Source != logs.SourceLocal && opts.Source != logs.SourcePublic {
			return logs.ListOptions{}, false, fmt.Errorf("invalid log source %q", source)
		}
	}
	if value := query.Get("status"); value != "" {
		status, err := strconv.Atoi(value)
		if err != nil || status < 100 || status > 599 {
			return logs.ListOptions{}, false, fmt.Errorf("invalid status value %q", value)
		}
		opts.Status = status
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit <= 0 {
			return logs.ListOptions{}, false, fmt.Errorf("invalid limit value %q", value)
		}
		opts.Limit = limit
	}
	if value := query.Get("since"); value != "" {
		since, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return logs.ListOptions{}, false, fmt.Errorf("invalid since value %q", value)
		}
		opts.Since = since
	}

	followText := query.Get("follow")
	if followText == "" {
		return opts, false, nil
	}
	follow, err := strconv.ParseBool(followText)
	if err != nil {
		return logs.ListOptions{}, false, fmt.Errorf("invalid follow value %q", followText)
	}
	return opts, follow, nil
}

func entriesAfter(entries []logs.Entry, lastID string) []logs.Entry {
	if lastID == "" {
		return entries
	}
	for i, entry := range entries {
		if entry.ID == lastID {
			return entries[i+1:]
		}
	}
	// The cursor fell out of the bounded ring. Every retained entry is newer
	// than it, so resume from the oldest entry still available.
	return entries
}
