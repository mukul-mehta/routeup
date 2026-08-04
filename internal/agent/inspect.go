// Inspect reads one retained request from the agent-local log ring:
//
//	routeup inspect -> GET /v1/requests/{id} -> logs.Store.Get -> JSON Entry
//
// Normal log listing deliberately omits headers and bodies. This endpoint is
// served only over the user's 0600 Unix socket and returns retained request data
// only for routes that explicitly enabled capture.
package agent

import (
	"net/http"
)

func (a *Agent) handleInspect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing request id", nil)
		return
	}
	entry, ok := a.logStore.Get(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "request not found", nil)
		return
	}
	if entry.Capture == nil {
		writeJSONError(w, http.StatusConflict, "request was not captured", nil)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}
