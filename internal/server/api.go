package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/mukul-mehta/routeup/internal/tunnel"
)

// handler builds the server's top-level HTTP handler. Requests under
// ControlPrefix are control plane (health and the tunnel endpoint the agent
// dials); everything else is request ingress, forwarded through a tunnel by Host
func (s *Server) handler() http.Handler {
	control := http.NewServeMux()
	control.HandleFunc("GET "+PathHealth, s.handleHealth)
	control.Handle(tunnel.Path, s.hub.AcceptHandler())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ControlPrefix || strings.HasPrefix(r.URL.Path, ControlPrefix+"/") {
			control.ServeHTTP(w, r)
			return
		}
		s.serveIngress(w, r)
	})
}

func (s *Server) serveIngress(w http.ResponseWriter, r *http.Request) {
	host := stripPort(r.Host)
	resp, err := s.hub.RoundTrip(host, r)
	if err != nil {
		if errors.Is(err, tunnel.ErrNoSession) {
			http.Error(w, "routeup: no tunnel is connected for "+host, http.StatusServiceUnavailable)
			return
		}
		s.logger.Warn("ingress roundtrip failed", "host", host, "err", err)
		http.Error(w, "routeup: tunnel error", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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

var hopByHopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func copyResponseHeaders(dst, src http.Header) {
	for _, h := range hopByHopHeaders {
		src.Del(h)
	}
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
