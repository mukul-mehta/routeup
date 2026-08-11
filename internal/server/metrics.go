package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var requestDurationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type serverMetrics struct {
	activeTunnels       atomic.Int64
	tunnelEstablished   atomic.Uint64
	tunnelClosed        atomic.Uint64
	claimsAccepted      atomic.Uint64
	claimsRejected      atomic.Uint64
	requestsByClass     [6]atomic.Uint64
	requestsInFlight    atomic.Int64
	requestDuration     [len(requestDurationBuckets)]atomic.Uint64
	requestDurationNsec atomic.Uint64
	requestCount        atomic.Uint64
	forwardErrors       atomic.Uint64
	reapedHolds         atomic.Uint64
}

func newServerMetrics() *serverMetrics {
	return &serverMetrics{}
}

func (m *serverMetrics) claimAccepted() {
	m.claimsAccepted.Add(1)
}

func (m *serverMetrics) claimRejected() {
	m.claimsRejected.Add(1)
}

func (m *serverMetrics) TunnelEstablished() {
	m.tunnelEstablished.Add(1)
	m.activeTunnels.Add(1)
}

func (m *serverMetrics) TunnelClosed() {
	m.tunnelClosed.Add(1)
	m.activeTunnels.Add(-1)
}

func (m *serverMetrics) TunnelForwardError() {
	m.forwardErrors.Add(1)
}

func (m *serverMetrics) requestStarted() {
	m.requestsInFlight.Add(1)
}

func (m *serverMetrics) requestCompleted(status int, duration time.Duration) {
	m.requestsInFlight.Add(-1)
	m.requestsByClass[statusClassIndex(status)].Add(1)
	m.requestCount.Add(1)
	if duration > 0 {
		m.requestDurationNsec.Add(uint64(duration))
	}
	seconds := duration.Seconds()
	for i, upperBound := range requestDurationBuckets {
		if seconds <= upperBound {
			m.requestDuration[i].Add(1)
		}
	}
}

func (m *serverMetrics) holdsReaped(count int) {
	if count > 0 {
		m.reapedHolds.Add(uint64(count))
	}
}

func statusClassIndex(status int) int {
	class := status / 100
	if class < 1 || class > 5 {
		return 5
	}
	return class - 1
}

func (m *serverMetrics) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", m.serveHTTP)
	return mux
}

func (m *serverMetrics) serveHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var out strings.Builder
	writeMetricHeader(&out, "routeup_tunnels_active", "Current number of active public tunnels.", "gauge")
	_, _ = fmt.Fprintf(&out, "routeup_tunnels_active %d\n", m.activeTunnels.Load())

	writeMetricHeader(&out, "routeup_tunnel_sessions_total", "Tunnel session lifecycle events.", "counter")
	_, _ = fmt.Fprintf(&out, "routeup_tunnel_sessions_total{event=\"established\"} %d\n", m.tunnelEstablished.Load())
	_, _ = fmt.Fprintf(&out, "routeup_tunnel_sessions_total{event=\"closed\"} %d\n", m.tunnelClosed.Load())

	writeMetricHeader(&out, "routeup_claims_total", "Public route claim outcomes.", "counter")
	_, _ = fmt.Fprintf(&out, "routeup_claims_total{result=\"accepted\"} %d\n", m.claimsAccepted.Load())
	_, _ = fmt.Fprintf(&out, "routeup_claims_total{result=\"rejected\"} %d\n", m.claimsRejected.Load())

	writeMetricHeader(&out, "routeup_http_requests_total", "Completed public ingress requests by status class.", "counter")
	classes := [...]string{"1xx", "2xx", "3xx", "4xx", "5xx", "other"}
	for i, class := range classes {
		_, _ = fmt.Fprintf(&out, "routeup_http_requests_total{status_class=%s} %d\n", strconv.Quote(class), m.requestsByClass[i].Load())
	}

	writeMetricHeader(&out, "routeup_http_requests_in_flight", "Current public ingress requests in flight.", "gauge")
	_, _ = fmt.Fprintf(&out, "routeup_http_requests_in_flight %d\n", m.requestsInFlight.Load())

	writeMetricHeader(&out, "routeup_http_request_duration_seconds", "Public ingress request duration.", "histogram")
	for i, upperBound := range requestDurationBuckets {
		_, _ = fmt.Fprintf(&out, "routeup_http_request_duration_seconds_bucket{le=%s} %d\n", strconv.Quote(strconv.FormatFloat(upperBound, 'g', -1, 64)), m.requestDuration[i].Load())
	}
	count := m.requestCount.Load()
	_, _ = fmt.Fprintf(&out, "routeup_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", count)
	_, _ = fmt.Fprintf(&out, "routeup_http_request_duration_seconds_sum %s\n", strconv.FormatFloat(float64(m.requestDurationNsec.Load())/float64(time.Second), 'g', -1, 64))
	_, _ = fmt.Fprintf(&out, "routeup_http_request_duration_seconds_count %d\n", count)

	writeMetricHeader(&out, "routeup_tunnel_forward_errors_total", "Public requests that failed while forwarding through a tunnel.", "counter")
	_, _ = fmt.Fprintf(&out, "routeup_tunnel_forward_errors_total %d\n", m.forwardErrors.Load())

	writeMetricHeader(&out, "routeup_holds_reaped_total", "Expired or stale route holds removed by the server.", "counter")
	_, _ = fmt.Fprintf(&out, "routeup_holds_reaped_total %d\n", m.reapedHolds.Load())

	_, _ = w.Write([]byte(out.String()))
}

func writeMetricHeader(out *strings.Builder, name, help, metricType string) {
	_, _ = fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, metricType)
}
