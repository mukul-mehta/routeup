package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// This file is the public server's HTTP surface. One http.Server serves two
// planes, split by URL path:
//
//	/_routeup/*  → control plane
//	    GET /_routeup/v1/health  → handleHealth
//	    /_routeup/tunnel         → tunnels.AcceptHandler() (the agent dials in;
//	                               this is where a tunnel is born)
//	everything else → public ingress
//	    serveIngress: strip the port from Host, find the tunnel holding that host
//	    (tunnels.Handler), and hand the request to its reverse proxy. No live
//	    tunnel → 503; a forward failure → 502 (returned by the proxy itself).
//
// Splitting by path prefix (not by Host) lets the server's own control hostname
// be an ordinary subdomain of the public domain without colliding with tunnels.

// handler builds the server's top-level HTTP handler. Requests under
// ControlPrefix are control plane (health and the tunnel endpoint the agent
// dials); everything else is request ingress, forwarded through a tunnel by Host
func (s *Server) handler() http.Handler {
	control := http.NewServeMux()
	// Health probe.
	control.HandleFunc("GET "+PathHealth, s.handleHealth)

	// The endpoint the agent dials to open its tunnel (WebSocket upgrade).
	control.Handle(tunnel.Path, s.tunnels.AcceptHandler())

	// Top-level router: /_routeup/* is control plane (the two handlers above);
	// everything else is public ingress, dispatched to a tunnel by Host.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ControlPrefix || strings.HasPrefix(r.URL.Path, ControlPrefix+"/") {
			control.ServeHTTP(w, r)
			return
		}
		s.serveIngress(w, r)
	})
}

// serveIngress dispatches a public request to the tunnel holding its Host. The
// per-session reverse proxy (built in newSessionProxy) does the forwarding,
// header hygiene, body streaming, and its own 502 on a dead tunnel; a Host with
// no live tunnel is a 503 here.
func (s *Server) serveIngress(w http.ResponseWriter, r *http.Request) {
	host := stripPort(r.Host)
	h, ok := s.tunnels.Handler(host)
	if !ok {
		http.Error(w, "routeup: no tunnel is connected for "+host, http.StatusServiceUnavailable)
		return
	}
	// h is the per-session reverse proxy (newSessionProxy). ServeHTTP runs it:
	// its http.Transport dials via session.Open() — a fresh yamux stream — and
	// net/http writes THIS request onto that stream, then reads the response back
	// over it. So the request hits the wire here, inside ServeHTTP.
	h.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:          "ok",
		Domain:          s.cfg.Domain,
		PublicNamespace: s.cfg.PublicNamespace,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func stripPort(h string) string {
	if h == "" {
		return h
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}
