package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func tokenReq(host, tokenID string) HoldRequest {
	return HoldRequest{Host: host, Kind: HoldByToken, TokenID: tokenID}
}

func nsReq(host string) HoldRequest {
	return HoldRequest{Host: host, Kind: HoldByNamespace}
}

// backdateGrace rewrites a released hold's grace deadline to now+d, letting a
// test put the grace window in the past (negative d) without waiting.
func backdateGrace(t *testing.T, s *Store, host string, d time.Duration) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE route_holds SET grace_until = ? WHERE host = ?`,
		time.Now().Add(d).UnixNano(), host); err != nil {
		t.Fatalf("backdate grace for %s: %v", host, err)
	}
}

func TestHold_FreeThenActiveConflict(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "api.alice.routeup.dev"

	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokA")); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	// same token re-claims: idempotent
	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokA")); err != nil {
		t.Errorf("same-token re-claim should succeed, got %v", err)
	}
	// different token: conflict
	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokB")); !errors.Is(err, ErrRouteConflict) {
		t.Errorf("different-token hold err = %v, want ErrRouteConflict", err)
	}
}

func TestHold_Namespace_NoGrace(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "foo.try.routeup.dev"

	if _, err := s.HoldRoute(ctx, nsReq(host)); err != nil {
		t.Fatalf("namespace hold: %v", err)
	}
	if _, err := s.HoldRoute(ctx, nsReq(host)); !errors.Is(err, ErrRouteConflict) {
		t.Errorf("second namespace hold err = %v, want ErrRouteConflict", err)
	}
	// release deletes immediately — no grace
	if err := s.Release(ctx, host); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, found, _ := s.GetHold(ctx, host); found {
		t.Errorf("namespace hold should be deleted on release")
	}
	if _, err := s.HoldRoute(ctx, nsReq(host)); err != nil {
		t.Errorf("re-claim after release should succeed, got %v", err)
	}
}

func TestHold_TokenGraceWindow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "api.alice.routeup.dev"

	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokA")); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, host); err != nil {
		t.Fatal(err)
	}

	// inside grace: a different token is still blocked
	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokB")); !errors.Is(err, ErrRouteConflict) {
		t.Errorf("in-grace different-token err = %v, want ErrRouteConflict", err)
	}
	// inside grace: the same token resumes, hold becomes active again
	resumed, err := s.HoldRoute(ctx, tokenReq(host, "tokA"))
	if err != nil {
		t.Fatalf("in-grace same-token resume: %v", err)
	}
	if resumed.State != holdStateActive || resumed.GraceUntil != nil {
		t.Errorf("resumed hold = %+v, want active with no grace", resumed)
	}
}

func TestHold_GraceExpiry(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	host := "api.alice.routeup.dev"

	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokA")); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, host); err != nil {
		t.Fatal(err)
	}

	backdateGrace(t, s, host, -time.Second) // grace already elapsed
	if _, err := s.HoldRoute(ctx, tokenReq(host, "tokB")); err != nil {
		t.Errorf("after grace, different token should hold freely, got %v", err)
	}
}

func TestReapExpiredHolds(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, err := s.HoldRoute(ctx, tokenReq("a.alice.routeup.dev", "tokA")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldRoute(ctx, tokenReq("b.alice.routeup.dev", "tokA")); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "a.alice.routeup.dev"); err != nil {
		t.Fatal(err)
	}

	// before grace elapses, reaper removes nothing
	if n, err := s.ReapExpiredHolds(ctx); err != nil || n != 0 {
		t.Errorf("early reap = (%d, %v), want (0, nil)", n, err)
	}

	backdateGrace(t, s, "a.alice.routeup.dev", -time.Second)
	n, err := s.ReapExpiredHolds(ctx)
	if err != nil || n != 1 {
		t.Errorf("reap = (%d, %v), want (1, nil)", n, err)
	}
	if _, found, _ := s.GetHold(ctx, "a.alice.routeup.dev"); found {
		t.Errorf("expired hold should be gone")
	}
	if _, found, _ := s.GetHold(ctx, "b.alice.routeup.dev"); !found {
		t.Errorf("active hold should remain")
	}
}

func TestPurgeEphemeralHolds(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, err := s.HoldRoute(ctx, nsReq("foo.try.routeup.dev")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldRoute(ctx, tokenReq("api.alice.routeup.dev", "tokA")); err != nil {
		t.Fatal(err)
	}

	n, err := s.PurgeEphemeralHolds(ctx)
	if err != nil || n != 1 {
		t.Errorf("purge = (%d, %v), want (1, nil)", n, err)
	}
	if _, found, _ := s.GetHold(ctx, "foo.try.routeup.dev"); found {
		t.Errorf("ephemeral hold should be purged")
	}
	if _, found, _ := s.GetHold(ctx, "api.alice.routeup.dev"); !found {
		t.Errorf("token hold should survive purge")
	}
}
