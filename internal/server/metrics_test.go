package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerMetricsPrometheusOutput(t *testing.T) {
	metrics := newServerMetrics()
	metrics.claimAccepted()
	metrics.claimRejected()
	metrics.TunnelEstablished()
	metrics.requestStarted()
	metrics.requestCompleted(http.StatusServiceUnavailable, 150*time.Millisecond, requestNoTunnel)
	metrics.requestStarted()
	metrics.requestCompleted(http.StatusOK, 300*time.Millisecond, requestForwarded)
	metrics.TunnelForwardError()
	metrics.holdsReaped(3)
	metrics.TunnelClosed()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metrics.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("content type = %q", got)
	}

	output := rec.Body.String()
	for _, want := range []string{
		"routeup_tunnels_active 0",
		`routeup_tunnel_sessions_total{event="established"} 1`,
		`routeup_tunnel_sessions_total{event="closed"} 1`,
		`routeup_claims_total{result="accepted"} 1`,
		`routeup_claims_total{result="rejected"} 1`,
		`routeup_http_requests_total{outcome="no_tunnel",status_class="5xx"} 1`,
		`routeup_http_requests_total{outcome="forwarded",status_class="2xx"} 1`,
		"routeup_http_requests_in_flight 0",
		`routeup_http_request_duration_seconds_bucket{le="0.25",outcome="no_tunnel"} 1`,
		`routeup_http_request_duration_seconds_bucket{le="+Inf",outcome="forwarded"} 1`,
		`routeup_http_request_duration_seconds_sum{outcome="no_tunnel"} 0.15`,
		`routeup_http_request_duration_seconds_count{outcome="forwarded"} 1`,
		"routeup_tunnel_forward_errors_total 1",
		"routeup_holds_reaped_total 3",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("metrics missing %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{"host=", "route=", "token=", "path=", "source_ip="} {
		if strings.Contains(output, forbidden) {
			t.Errorf("metrics contain identifying label %q:\n%s", forbidden, output)
		}
	}
}
