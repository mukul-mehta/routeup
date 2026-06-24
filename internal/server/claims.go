package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const graceWindow = 30 * time.Second

var ErrClaimConflict = errors.New("route already claimed")

type ClaimKind string

const (
	ClaimToken     ClaimKind = "token"
	ClaimNamespace ClaimKind = "namespace"
)

const (
	claimStateActive   = "active"
	claimStateReleased = "released"
)

type ClaimRequest struct {
	Host    string
	Kind    ClaimKind
	TokenID string
}

type Claim struct {
	Host       string
	TokenID    string
	Kind       ClaimKind
	State      string
	Ephemeral  bool
	ClaimedAt  time.Time
	GraceUntil *time.Time
}

func (s *Store) Claim(ctx context.Context, req ClaimRequest) (Claim, error) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	now := time.Now().UTC()
	existing, found, err := s.GetClaim(ctx, req.Host)
	if err != nil {
		return Claim{}, err
	}
	if found && claimBlocked(existing, req, now) {
		return Claim{}, ErrClaimConflict
	}

	c := Claim{
		Host:      req.Host,
		TokenID:   req.TokenID,
		Kind:      req.Kind,
		State:     claimStateActive,
		Ephemeral: req.Kind == ClaimNamespace,
		ClaimedAt: now,
	}
	if err := s.upsertClaim(ctx, c); err != nil {
		return Claim{}, err
	}
	return c, nil
}

// Release ends a hold. Namespace (ephemeral) claims are deleted immediately;
// token claims enter the grace window, resumable by the same token until it
// elapses.
func (s *Store) Release(ctx context.Context, host string) error {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	existing, found, err := s.GetClaim(ctx, host)
	if err != nil || !found {
		return err
	}
	if existing.Ephemeral || existing.Kind == ClaimNamespace {
		return s.deleteClaim(ctx, host)
	}

	grace := time.Now().UTC().Add(graceWindow)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE claims SET state = ?, grace_until = ? WHERE host = ?`,
		claimStateReleased, grace.UnixNano(), host); err != nil {
		return fmt.Errorf("release claim: %w", err)
	}
	return nil
}

// ReapExpiredClaims deletes released token claims whose grace window has
// elapsed. It returns the number removed.
func (s *Store) ReapExpiredClaims(ctx context.Context) (int, error) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	res, err := s.db.ExecContext(ctx,
		`DELETE FROM claims WHERE state = ? AND grace_until IS NOT NULL AND grace_until < ?`,
		claimStateReleased, time.Now().UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("reap claims: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// PurgeEphemeralClaims deletes all namespace (session-only) claims. The server
// runs this at startup: anonymous sessions do not survive a restart.
func (s *Store) PurgeEphemeralClaims(ctx context.Context) (int, error) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM claims WHERE ephemeral = 1`)
	if err != nil {
		return 0, fmt.Errorf("purge ephemeral claims: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// GetClaim returns the claim for host, if any.
func (s *Store) GetClaim(ctx context.Context, host string) (Claim, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT host, token_id, kind, state, ephemeral, claimed_at, grace_until
		 FROM claims WHERE host = ?`, host)

	var (
		c       Claim
		tokenID sql.NullString
		kind    string
		state   string
		eph     int
		claimed int64
		grace   sql.NullInt64
	)
	if err := row.Scan(&c.Host, &tokenID, &kind, &state, &eph, &claimed, &grace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Claim{}, false, nil
		}
		return Claim{}, false, fmt.Errorf("scan claim: %w", err)
	}
	c.TokenID = tokenID.String
	c.Kind = ClaimKind(kind)
	c.State = state
	c.Ephemeral = eph != 0
	c.ClaimedAt = time.Unix(0, claimed).UTC()
	c.GraceUntil = nullNanosToTime(grace)
	return c, true, nil
}

func (s *Store) upsertClaim(ctx context.Context, c Claim) error {
	var tokenID any
	if c.TokenID != "" {
		tokenID = c.TokenID
	}
	var grace any
	if c.GraceUntil != nil {
		grace = c.GraceUntil.UnixNano()
	}
	eph := 0
	if c.Ephemeral {
		eph = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO claims (host, token_id, kind, state, ephemeral, claimed_at, grace_until)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET
		   token_id = excluded.token_id, kind = excluded.kind, state = excluded.state,
		   ephemeral = excluded.ephemeral, claimed_at = excluded.claimed_at,
		   grace_until = excluded.grace_until`,
		c.Host, tokenID, string(c.Kind), c.State, eph, c.ClaimedAt.UnixNano(), grace); err != nil {
		return fmt.Errorf("upsert claim: %w", err)
	}
	return nil
}

func (s *Store) deleteClaim(ctx context.Context, host string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM claims WHERE host = ?`, host); err != nil {
		return fmt.Errorf("delete claim: %w", err)
	}
	return nil
}

// claimBlocked reports whether an existing claim blocks req at time now.
//
//   - A namespace claim is session-only: any active holder blocks (released
//     namespace claims are deleted, so a found namespace claim is active).
//   - An active token claim blocks everyone but the same token (which resumes).
//   - A released token claim blocks others only while inside its grace window;
//     once grace elapses the host is free.
func claimBlocked(existing Claim, req ClaimRequest, now time.Time) bool {
	if existing.Kind == ClaimNamespace {
		return existing.State == claimStateActive
	}

	sameToken := req.Kind == ClaimToken && req.TokenID != "" && existing.TokenID == req.TokenID
	if existing.State == claimStateActive {
		return !sameToken
	}

	inGrace := existing.GraceUntil != nil && now.Before(*existing.GraceUntil)
	if inGrace {
		return !sameToken
	}
	return false
}
