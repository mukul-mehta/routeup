package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func tokenReq(host, tokenID string) ClaimRequest {
	return ClaimRequest{Host: host, Kind: ClaimToken, TokenID: tokenID}
}

func nsReq(host string) ClaimRequest {
	return ClaimRequest{Host: host, Kind: ClaimNamespace}
}

// backdateGrace rewrites a released claim's grace deadline to now+d, letting a
// test put the grace window in the past (negative d) without waiting.
func backdateGrace(t *testing.T, s *Store, host string, d time.Duration) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE claims SET grace_until = ? WHERE host = ?`,
		time.Now().Add(d).UnixNano(), host); err != nil {
		t.Fatalf("backdate grace for %s: %v", host, err)
	}
}

func TestClaim_FreeThenActiveConflict(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "api.alice.routeup.dev"

	if _, err := s.Claim(ctx, tokenReq(host, "tokA")); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// same token re-claims: idempotent
	if _, err := s.Claim(ctx, tokenReq(host, "tokA")); err != nil {
		t.Errorf("same-token re-claim should succeed, got %v", err)
	}
	// different token: conflict
	if _, err := s.Claim(ctx, tokenReq(host, "tokB")); !errors.Is(err, ErrClaimConflict) {
		t.Errorf("different-token claim err = %v, want ErrClaimConflict", err)
	}
}

func TestClaim_Namespace_NoGrace(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "foo.try.routeup.dev"

	if _, err := s.Claim(ctx, nsReq(host)); err != nil {
		t.Fatalf("namespace claim: %v", err)
	}
	if _, err := s.Claim(ctx, nsReq(host)); !errors.Is(err, ErrClaimConflict) {
		t.Errorf("second namespace claim err = %v, want ErrClaimConflict", err)
	}
	// release deletes immediately — no grace
	if err := s.Release(ctx, host); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, found, _ := s.GetClaim(ctx, host); found {
		t.Errorf("namespace claim should be deleted on release")
	}
	if _, err := s.Claim(ctx, nsReq(host)); err != nil {
		t.Errorf("re-claim after release should succeed, got %v", err)
	}
}

func TestClaim_TokenGraceWindow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "api.alice.routeup.dev"

	if _, err := s.Claim(ctx, tokenReq(host, "tokA")); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, host); err != nil {
		t.Fatal(err)
	}

	// inside grace: a different token is still blocked
	if _, err := s.Claim(ctx, tokenReq(host, "tokB")); !errors.Is(err, ErrClaimConflict) {
		t.Errorf("in-grace different-token err = %v, want ErrClaimConflict", err)
	}
	// inside grace: the same token resumes, claim becomes active again
	resumed, err := s.Claim(ctx, tokenReq(host, "tokA"))
	if err != nil {
		t.Fatalf("in-grace same-token resume: %v", err)
	}
	if resumed.State != claimStateActive || resumed.GraceUntil != nil {
		t.Errorf("resumed claim = %+v, want active with no grace", resumed)
	}
}

func TestClaim_GraceExpiry(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "api.alice.routeup.dev"

	if _, err := s.Claim(ctx, tokenReq(host, "tokA")); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, host); err != nil {
		t.Fatal(err)
	}

	backdateGrace(t, s, host, -time.Second) // grace already elapsed
	if _, err := s.Claim(ctx, tokenReq(host, "tokB")); err != nil {
		t.Errorf("after grace, different token should claim freely, got %v", err)
	}
}

func TestReapExpiredClaims(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, err := s.Claim(ctx, tokenReq("a.alice.routeup.dev", "tokA")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, tokenReq("b.alice.routeup.dev", "tokA")); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "a.alice.routeup.dev"); err != nil {
		t.Fatal(err)
	}

	// before grace elapses, reaper removes nothing
	if n, err := s.ReapExpiredClaims(ctx); err != nil || n != 0 {
		t.Errorf("early reap = (%d, %v), want (0, nil)", n, err)
	}

	backdateGrace(t, s, "a.alice.routeup.dev", -time.Second)
	n, err := s.ReapExpiredClaims(ctx)
	if err != nil || n != 1 {
		t.Errorf("reap = (%d, %v), want (1, nil)", n, err)
	}
	if _, found, _ := s.GetClaim(ctx, "a.alice.routeup.dev"); found {
		t.Errorf("expired claim should be gone")
	}
	if _, found, _ := s.GetClaim(ctx, "b.alice.routeup.dev"); !found {
		t.Errorf("active claim should remain")
	}
}

func TestPurgeEphemeralClaims(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, err := s.Claim(ctx, nsReq("foo.try.routeup.dev")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, tokenReq("api.alice.routeup.dev", "tokA")); err != nil {
		t.Fatal(err)
	}

	n, err := s.PurgeEphemeralClaims(ctx)
	if err != nil || n != 1 {
		t.Errorf("purge = (%d, %v), want (1, nil)", n, err)
	}
	if _, found, _ := s.GetClaim(ctx, "foo.try.routeup.dev"); found {
		t.Errorf("ephemeral claim should be purged")
	}
	if _, found, _ := s.GetClaim(ctx, "api.alice.routeup.dev"); !found {
		t.Errorf("token claim should survive purge")
	}
}
