// Package proxy implements the local HTTP reverse proxy used by the routeup
// agent.
//
// One handler maps an incoming request to a registered local target by
// inspecting the request's Host header, stripping any port suffix, and
// dropping the LocalSuffix (".localhost"). The remaining label sequence is
// the route name, which is looked up against the agent's registry.
//
// Both local and tunnel handlers finish by recording one metadata-only entry:
//
//	request -> route/path lookup -> upstream reverse proxy -> logs.Store
package proxy

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mukul-mehta/routeup/internal/logs"
	"github.com/mukul-mehta/routeup/internal/route"
)

// localTargetHost is the host the proxy dials for upstreams. Using "localhost"
// rather than "127.0.0.1" lets Go try both IPv4 and IPv6 loopback: dev servers
// often bind to localhost and may end up on ::1 only, which a hardcoded
// 127.0.0.1 would never reach.
const localTargetHost = "localhost"

// TargetLookup is the minimal behavior the proxy needs from the registry.
//
// It exists to break an import cycle: the agent package imports proxy (it wires
// proxy.New onto its TCP listener), so proxy must not import agent in return.
// Depending on this interface instead of the concrete *agent.Registry keeps the
// dependency one-directional.
type TargetLookup interface {
	LookupTargets(name string) (targets []route.Target, captureRequest bool, captureResponse bool, redactHeaders []string, ok bool)
}

// New returns an HTTP handler that reverse-proxies requests to the registered
// local target identified by Host. Unknown hosts get a small text/plain 404.
func New(lookup TargetLookup, logStore *logs.Store, logger *slog.Logger) http.Handler {
	logger = defaultLogger(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := stripPort(r.Host)

		name, ok := routeNameFromHost(host)
		if !ok {
			writeNotFound(w, host, "host is not a *."+route.LocalSuffix+" name")
			return
		}

		targets, captureRequest, captureResponse, redactHeaders, ok := lookup.LookupTargets(name)
		if !ok {
			writeNotFound(w, host, "no route is currently registered for "+name)
			return
		}

		serveTargets(w, r, targets, nil, captureRequest, captureResponse, redactHeaders, logStore, logger, logs.Entry{
			Source: logs.SourceLocal,
			Route:  name,
			Host:   host,
		})
	})
}

// NewTargets returns a handler that path-routes requests across targets. When
// exposedPaths is non-empty, requests outside those public paths return 404.
// routeName is the dotted local route, not the normalized public claim label.
func NewTargets(targets []route.Target, exposedPaths []string, routeName string, captureRequest bool, captureResponse bool, redactHeaders []string, logStore *logs.Store, logger *slog.Logger) http.Handler {
	logger = defaultLogger(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveTargets(w, r, targets, exposedPaths, captureRequest, captureResponse, redactHeaders, logStore, logger, logs.Entry{
			Source: logs.SourcePublic,
			Route:  routeName,
			Host:   stripPort(r.Host),
		})
	})
}

func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func serveTargets(w http.ResponseWriter, r *http.Request, targets []route.Target, exposedPaths []string, captureRequest bool, captureResponse bool, redactHeaders []string, logStore *logs.Store, logger *slog.Logger, entry logs.Entry) {
	startedAt := time.Now()
	entry.StartedAt = startedAt
	entry.Method = r.Method
	entry.RequestPath = r.URL.RequestURI()
	response := &statusWriter{ResponseWriter: w}
	var target route.Target
	var requestCapture *logs.MessageCapture
	var responseCapture *logs.MessageCapture

	defer func() {
		if logStore == nil {
			return
		}
		entry.Duration = time.Since(startedAt)
		entry.Status = response.status
		entry.Target = target
		if requestCapture != nil || responseCapture != nil {
			c := &logs.Capture{}
			if requestCapture != nil {
				captured := requestCapture.Take()
				c.Request = &captured
			}
			if responseCapture != nil {
				captured := responseCapture.Take()
				c.Response = &captured
			}
			entry.Capture = c
		}
		if _, err := logStore.Append(entry); err != nil {
			logger.Warn("record request log", "route", entry.Route, "err", err)
		}
	}()

	if !route.PathAllowed(exposedPaths, r.URL.Path) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(response, "routeup: path is not exposed\n")
		return
	}

	var ok bool
	target, ok = route.MatchTarget(targets, r.URL.Path)
	if !ok {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(response, "routeup: no target for path %q\n", r.URL.Path)
		return
	}
	if captureRequest {
		requestCapture = logs.NewMessageCapture(r.Header, r.Body, redactHeaders)
		r.Body = requestCapture
	}

	// See localTargetHost for why this is a hostname, not a literal IP.
	targetURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(localTargetHost, strconv.Itoa(target.Port)),
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(targetURL)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Warn("proxy upstream error",
				"host", entry.Host, "name", entry.Route, "path", target.Path, "port", target.Port, "err", err)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(w, "routeup: upstream %s failed: %v\n", targetURL.Host, err)
		},
		ModifyResponse: func(upstream *http.Response) error {
			response.status = upstream.StatusCode
			if captureResponse {
				responseCapture = logs.NewMessageCapture(upstream.Header, upstream.Body, redactHeaders)
				upstream.Body = responseCapture
			}
			return nil
		},
	}
	rp.ServeHTTP(response, r)
}

// statusWriter observes the status emitted by the proxy without changing the
// response stream. Unwrap lets http.ResponseController reach the underlying
// writer for SSE flushing and WebSocket hijacking.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *statusWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
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
