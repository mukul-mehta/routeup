package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMultiLimiter_Disabled(t *testing.T) {
	l := newMultiLimiter(0, 0)
	for range 1000 {
		if !l.allow("key") {
			t.Fatal("disabled limiter (rate=0) must always allow")
		}
	}
}

func TestMultiLimiter_Burst(t *testing.T) {
	l := newMultiLimiter(1, 3) // 1 token/s, burst of 3
	allowed := 0
	for range 10 {
		if l.allow("key") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("want 3 allowed (burst), got %d", allowed)
	}
}

func TestMultiLimiter_KeyIsolation(t *testing.T) {
	l := newMultiLimiter(1, 1) // burst of 1 per key
	if !l.allow("a") {
		t.Fatal("first allow(a) should pass")
	}
	if !l.allow("b") {
		t.Fatal("first allow(b) should pass; b has its own independent bucket")
	}
	if l.allow("a") {
		t.Fatal("a's bucket is empty; second allow(a) should deny")
	}
	if l.allow("b") {
		t.Fatal("b's bucket is empty; second allow(b) should deny")
	}
}

func TestServeIngress_RequestRateLimit(t *testing.T) {
	store := openTestStore(t)
	cfg := ServerConfig{
		Domain: "routeup.dev",
		Listen: ":0",
		DBPath: "x",
		RateLimit: RateLimiterConfig{
			RequestRate:  1,
			RequestBurst: 1,
		},
	}
	srv := newServerWithStore(t, cfg, store)
	h := srv.handler()

	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = "myapp.routeup.dev"
		return r
	}

	// First request consumes the single burst token. No tunnel is active so the
	// server responds 503, but the limiter allowed it through.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("first request: want 503 (no tunnel), got %d", rec.Code)
	}

	// Second immediate request finds an empty bucket and gets 429.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: want 429 (rate limited), got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response is missing Retry-After header")
	}
}

func TestServeIngress_RateLimitCountsMetric(t *testing.T) {
	store := openTestStore(t)
	cfg := ServerConfig{
		Domain: "routeup.dev",
		Listen: ":0",
		DBPath: "x",
		RateLimit: RateLimiterConfig{
			RequestRate:  1,
			RequestBurst: 1,
		},
	}
	srv := newServerWithStore(t, cfg, store)
	h := srv.handler()

	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = "myapp.routeup.dev"
		return r
	}

	before := srv.metrics.rateLimitedRequests.Load()
	h.ServeHTTP(httptest.NewRecorder(), newReq()) // allowed
	h.ServeHTTP(httptest.NewRecorder(), newReq()) // rate limited
	h.ServeHTTP(httptest.NewRecorder(), newReq()) // rate limited

	after := srv.metrics.rateLimitedRequests.Load()
	if after-before != 2 {
		t.Fatalf("want 2 rate-limited requests recorded, got %d", after-before)
	}
}
