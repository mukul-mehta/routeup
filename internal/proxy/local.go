// Package proxy implements the local HTTP reverse proxy used by the routeup
// agent.
//
// One handler maps an incoming request to a registered local target by
// inspecting the request's Host header, stripping any port suffix, and
// dropping the LocalSuffix (".localhost"). The remaining label sequence is
// the route name, which is looked up against the agent's registry.
package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/mukul-mehta/routeup/internal/route"
)

// localTargetHost is the host the proxy dials for upstreams. Using "localhost"
// rather than "127.0.0.1" lets Go try both IPv4 and IPv6 loopback: dev servers
// often bind to localhost and may end up on ::1 only, which a hardcoded
// 127.0.0.1 would never reach.
const localTargetHost = "localhost"

// PortLookup is the minimal behavior the proxy needs from the registry.
//
// It exists to break an import cycle: the agent package imports proxy (it wires
// proxy.New onto its TCP listener), so proxy must not import agent in return.
// Depending on this interface instead of the concrete *agent.Registry keeps the
// dependency one-directional.
type PortLookup interface {
	LookupPort(name string) (port int, ok bool)
}

// New returns an HTTP handler that reverse-proxies requests to the registered
// local target identified by Host. Unknown hosts get a small text/plain 404.
func New(lookup PortLookup, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := stripPort(r.Host)

		name, ok := routeNameFromHost(host)
		if !ok {
			writeNotFound(w, host, "host is not a *."+route.LocalSuffix+" name")
			return
		}

		port, ok := lookup.LookupPort(name)
		if !ok {
			writeNotFound(w, host, "no route is currently registered for "+name)
			return
		}

		// See localTargetHost for why this is a hostname, not a literal IP.
		target := &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(localTargetHost, strconv.Itoa(port)),
		}
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				logger.Warn("proxy upstream error",
					"host", host, "name", name, "port", port, "err", err)
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = fmt.Fprintf(w, "routeup: upstream %s failed: %v\n", target.Host, err)
			},
		}
		rp.ServeHTTP(w, r)
	})
}

// stripPort removes a trailing :port from h, if any. IPv6 hosts (with
// brackets) and bare ports are handled by net.SplitHostPort with a fallback.
func stripPort(h string) string {
	if h == "" {
		return h
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// routeNameFromHost returns the route name embedded in host. It only accepts
// hosts ending in "." + route.LocalSuffix; the remaining portion is treated
// as the route name and must be non-empty.
func routeNameFromHost(host string) (string, bool) {
	host = strings.ToLower(host)
	suffix := "." + route.LocalSuffix
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	name := strings.TrimSuffix(host, suffix)
	if name == "" {
		return "", false
	}
	return name, true
}

func writeNotFound(w http.ResponseWriter, host, reason string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintf(w,
		"routeup: no route for host %q\nreason: %s\n\nrun `routeup routes` to see active routes.\n",
		host, reason)
}
