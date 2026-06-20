package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// apiHandler builds the /v1/* mux served over the agent's Unix socket.
// Wire format: JSON over HTTP/1.1. Versioned under /v1/. Authentication is
// filesystem permissions on the socket; no in-band auth.
func (a *Agent) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ipc.PathRoutes, a.handleRegister)
	mux.HandleFunc("DELETE "+ipc.PathRoutes+"/{name}", a.handleUnregister)
	mux.HandleFunc("GET "+ipc.PathRoutes, a.handleList)
	mux.HandleFunc("GET "+ipc.PathStatus, a.handleStatus)
	mux.HandleFunc("POST "+ipc.PathShutdown, a.handleShutdown)
	return mux
}

func (a *Agent) handleRegister(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	var in ipc.Claim
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error(), nil)
		return
	}
	if in.Name == "" || in.Port == 0 || in.OwnerPID == 0 {
		writeJSONError(w, http.StatusBadRequest, "name, port, owner_pid are required", nil)
		return
	}

	claim, err := a.reg.Register(in)
	if err != nil {
		if ce, ok := errors.AsType[*ipc.ConflictError](err); ok {
			writeJSONError(w, http.StatusConflict, "route held", &ce.Existing)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}

	a.logger.Info("route registered",
		"name", claim.Name, "port", claim.Port, "owner_pid", claim.OwnerPID)
	writeJSON(w, http.StatusCreated, claim)
}

func (a *Agent) handleUnregister(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "missing route name", nil)
		return
	}
	if a.reg.Unregister(name) {
		a.logger.Info("route unregistered", "name", name)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Agent) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"routes": a.reg.List()})
}

func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := ipc.Status{
		Version:       a.version,
		UptimeSeconds: int64(time.Since(a.startedAt).Seconds()),
		TLSAddr:       a.tlsListenAddr,
		BootID:        a.bootID,
		ExecPath:      a.execPath,
		ExecModTime:   a.execModTime,
	}
	writeJSON(w, http.StatusOK, status)
}

// handleShutdown acknowledges the request, flushes the response, then triggers
// graceful shutdown. The CLI uses this for `routeup agent stop` and for the
// build-staleness restart path. The 0600 socket is the only access control.
func (a *Agent) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	a.triggerShutdown()
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string, owner *ipc.Claim) {
	body := ipc.ErrorBody{Error: msg}
	if owner != nil {
		body.OwnerPID = owner.OwnerPID
		body.OwnerCWD = owner.OwnerCWD
	}
	writeJSON(w, status, body)
}
