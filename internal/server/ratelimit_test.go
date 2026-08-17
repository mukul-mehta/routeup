package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mukul-mehta/routeup/internal/tunnel"
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

func TestMultiLimiter_EvictsIdleKeys(t *testing.T) {
	now := time.Unix(1, 0)
	l := newMultiLimiter(1, 1)
	l.now = func() time.Time { return now }
	l.idleTTL = time.Minute

	if !l.allow("old") {
		t.Fatal("first old-key request should pass")
	}
	now = now.Add(time.Minute)
	if !l.allow("new") {
		t.Fatal("first new-key request should pass")
	}
	if _, ok := l.limiters["old"]; ok {
		t.Fatal("idle key was not evicted")
	}
}

func TestMultiLimiter_BoundsKeyCount(t *testing.T) {
	now := time.Unix(1, 0)
	l := newMultiLimiter(1, 1)
	l.now = func() time.Time { return now }
	l.maxKeys = 2

	if !l.allow("oldest") {
		t.Fatal("first oldest-key request should pass")
	}
	now = now.Add(time.Second)
	if !l.allow("newer") {
		t.Fatal("first newer-key request should pass")
	}
	now = now.Add(time.Second)
	if !l.allow("newest") {
		t.Fatal("first newest-key request should pass")
	}
	if len(l.limiters) != 2 {
		t.Fatalf("key count = %d, want 2", len(l.limiters))
	}
	if _, ok := l.limiters["oldest"]; ok {
		t.Fatal("oldest key was not evicted at capacity")
	}
}

func TestMultiLimiter_RefreshesRecentlyUsedKey(t *testing.T) {
	now := time.Unix(1, 0)
	l := newMultiLimiter(1, 1)
	l.now = func() time.Time { return now }
	l.maxKeys = 2

	if !l.allow("a") {
		t.Fatal("first a request should pass")
	}
	now = now.Add(time.Second)
	if !l.allow("b") {
		t.Fatal("first b request should pass")
	}
	now = now.Add(time.Second)
	if !l.allow("a") {
		t.Fatal("refreshed a request should pass")
	}
	now = now.Add(time.Second)
	if !l.allow("c") {
		t.Fatal("first c request should pass")
	}
	if _, ok := l.limiters["a"]; !ok {
		t.Fatal("recently used key a was evicted")
	}
	if _, ok := l.limiters["b"]; ok {
		t.Fatal("least-recently used key b was not evicted")
	}
}

func TestServeIngress_RequestRateLimit(t *testing.T) {
	publicURL, host, _, cleanup := startIngressTunnelWithRateLimit(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }),
		RateLimiterConfig{
			RequestRate:  1,
			RequestBurst: 1,
		},
	)
	defer cleanup()

	newReq := func() *http.Request {
		r, err := http.NewRequest(http.MethodGet, publicURL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		return r
	}

	resp, err := http.DefaultClient.Do(newReq())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", resp.StatusCode)
	}

	resp, err = http.DefaultClient.Do(newReq())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request: want 429 (rate limited), got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 response is missing Retry-After header")
	}
}

func TestServeIngress_RateLimitCountsMetric(t *testing.T) {
	publicURL, host, srv, cleanup := startIngressTunnelWithRateLimit(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }),
		RateLimiterConfig{
			RequestRate:  1,
			RequestBurst: 1,
		},
	)
	defer cleanup()

	newReq := func() *http.Request {
		r, err := http.NewRequest(http.MethodGet, publicURL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		return r
	}

	before := srv.metrics.rateLimitedRequests.Load()
	for range 3 {
		resp, err := http.DefaultClient.Do(newReq())
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	after := srv.metrics.rateLimitedRequests.Load()
	if after-before != 2 {
		t.Fatalf("want 2 rate-limited requests recorded, got %d", after-before)
	}
}

func TestServeIngress_DoesNotAllocateLimiterForUnknownHost(t *testing.T) {
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
	for range 2 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "unknown.routeup.dev"
		srv.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	}
	if len(srv.requestLimiter.limiters) != 0 {
		t.Fatalf("unknown host allocated %d limiter keys", len(srv.requestLimiter.limiters))
	}
}

func TestRouteBroker_UsesAuthenticatedTokenIDAsLimiterKey(t *testing.T) {
	store := openTestStore(t)
	id, secret, err := store.CreateToken(context.Background(), "alice", []AllowPattern{allowPattern(t, "*.alice.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	broker := newRateLimitBroker(store, RateLimiterConfig{ClaimRate: 1, ClaimBurst: 1})

	if _, err := broker.Hold(context.Background(), secret, tunnel.ClaimSpec{Route: "one"}); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err = broker.Hold(context.Background(), secret, tunnel.ClaimSpec{Route: "two"})
	assertCodedStatus(t, err, http.StatusTooManyRequests)

	if _, ok := broker.claimLimiter.limiters["token:"+id]; !ok {
		t.Fatalf("limiter does not contain authenticated token ID %q", id)
	}
	if _, ok := broker.claimLimiter.limiters[secret]; ok {
		t.Fatal("limiter retained the plaintext token secret")
	}
}

func TestRouteBroker_InvalidTokensShareOneLimiterKey(t *testing.T) {
	store := openTestStore(t)
	broker := newRateLimitBroker(store, RateLimiterConfig{ClaimRate: 1, ClaimBurst: 1})

	_, err := broker.Hold(context.Background(), "sk_routeup_invalid-one", tunnel.ClaimSpec{Route: "one"})
	assertCodedStatus(t, err, http.StatusUnauthorized)
	_, err = broker.Hold(context.Background(), "sk_routeup_invalid-two", tunnel.ClaimSpec{Route: "two"})
	assertCodedStatus(t, err, http.StatusTooManyRequests)

	if len(broker.claimLimiter.limiters) != 1 {
		t.Fatalf("invalid tokens allocated %d keys, want 1", len(broker.claimLimiter.limiters))
	}
	if _, ok := broker.claimLimiter.limiters["invalid"]; !ok {
		t.Fatal("invalid-token bucket is missing")
	}
}

func TestRouteBroker_AnonymousClaimsUseGlobalLimiter(t *testing.T) {
	store := openTestStore(t)
	broker := newRateLimitBroker(store, RateLimiterConfig{AnonRate: 1, AnonBurst: 1})

	if _, err := broker.Hold(context.Background(), "", tunnel.ClaimSpec{Route: "one"}); err != nil {
		t.Fatalf("first anonymous claim: %v", err)
	}
	_, err := broker.Hold(context.Background(), "", tunnel.ClaimSpec{Route: "two"})
	assertCodedStatus(t, err, http.StatusTooManyRequests)
}

func newRateLimitBroker(store *Store, limits RateLimiterConfig) *routeBroker {
	cfg := ServerConfig{Domain: "routeup.dev", PublicNamespace: "try"}
	return &routeBroker{
		authorizer:   NewAuthorizer(cfg, store),
		store:        store,
		metrics:      newServerMetrics(),
		claimLimiter: newMultiLimiter(limits.ClaimRate, limits.ClaimBurst),
		anonLimiter:  newMultiLimiter(limits.AnonRate, limits.AnonBurst),
	}
}

func assertCodedStatus(t *testing.T, err error, want int) {
	t.Helper()
	var coded *codedError
	if !errors.As(err, &coded) {
		t.Fatalf("error = %v, want coded status %d", err, want)
	}
	if coded.StatusCode() != want {
		t.Fatalf("status = %d, want %d", coded.StatusCode(), want)
	}
}
