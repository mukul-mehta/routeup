package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const graceWindow = 30 * time.Second

// ErrRouteConflict means a route is already held by someone else.
var ErrRouteConflict = errors.New("route already claimed")

// HoldKind records how a route is held: by an authenticated token, or by the
// anonymous public namespace.
type HoldKind string

const (
	HoldByToken     HoldKind = "token"
	HoldByNamespace HoldKind = "namespace"
)

const (
	holdStateActive   = "active"
	holdStateReleased = "released"
)

// HoldRequest is the input to HoldRoute: which host to hold, how, and for whom.
type HoldRequest struct {
	Host    string
	Kind    HoldKind
	TokenID string
}

// RouteHold is a persisted claim on a public host: the database row that records
// who holds a route and in what state. (Distinct from tunnel.ClaimSpec, which is
// the wire request that asks for one.)
type RouteHold struct {
	Host       string
	TokenID    string
	Kind       HoldKind
	State      string
	Ephemeral  bool
	HeldAt     time.Time
	GraceUntil *time.Time
}

// HoldRoute holds req.Host for the requester, or returns ErrRouteConflict if a
// live hold already blocks it.
func (s *Store) HoldRoute(ctx context.Context, req HoldRequest) (RouteHold, error) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()

	now := time.Now().UTC()
	existing, found, err := s.GetHold(ctx, req.Host)
	if err != nil {
		return RouteHold{}, err
	}
	if found && holdBlocked(existing, req, now) {
		return RouteHold{}, ErrRouteConflict
	}

	h := RouteHold{
		Host:      req.Host,
		TokenID:   req.TokenID,
		Kind:      req.Kind,
		State:     holdStateActive,
		Ephemeral: req.Kind == HoldByNamespace,
		HeldAt:    now,
	}
	if err := s.upsertHold(ctx, h); err != nil {
		return RouteHold{}, err
	}
	return h, nil
}

// Release ends a hold. Namespace (ephemeral) holds are deleted immediately;
// token holds enter the grace window, resumable by the same token until it
// elapses.
func (s *Store) Release(ctx context.Context, host string) error {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()

	existing, found, err := s.GetHold(ctx, host)
	if err != nil || !found {
		return err
	}
	if existing.Ephemeral || existing.Kind == HoldByNamespace {
		return s.deleteHold(ctx, host)
	}

	grace := time.Now().UTC().Add(graceWindow)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE route_holds SET state = ?, grace_until = ? WHERE host = ?`,
		holdStateReleased, grace.UnixNano(), host); err != nil {
		return fmt.Errorf("release hold: %w", err)
	}
	return nil
}

// ReapExpiredHolds deletes released token holds whose grace window has elapsed.
// It returns the number removed.
func (s *Store) ReapExpiredHolds(ctx context.Context) (int, error) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()

	res, err := s.db.ExecContext(ctx,
		`DELETE FROM route_holds WHERE state = ? AND grace_until IS NOT NULL AND grace_until < ?`,
		holdStateReleased, time.Now().UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("reap holds: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// PurgeEphemeralHolds deletes all namespace (session-only) holds. The server
// runs this at startup: anonymous sessions do not survive a restart.
func (s *Store) PurgeEphemeralHolds(ctx context.Context) (int, error) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM route_holds WHERE ephemeral = 1`)
	if err != nil {
		return 0, fmt.Errorf("purge ephemeral holds: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// GetHold returns the hold for host, if any.
func (s *Store) GetHold(ctx context.Context, host string) (RouteHold, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT host, token_id, kind, state, ephemeral, held_at, grace_until
		 FROM route_holds WHERE host = ?`, host)

	var (
		h       RouteHold
		tokenID sql.NullString
		kind    string
		state   string
		eph     int
		held    int64
		grace   sql.NullInt64
	)
	if err := row.Scan(&h.Host, &tokenID, &kind, &state, &eph, &held, &grace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RouteHold{}, false, nil
		}
		return RouteHold{}, false, fmt.Errorf("scan hold: %w", err)
	}
	h.TokenID = tokenID.String
	h.Kind = HoldKind(kind)
	h.State = state
	h.Ephemeral = eph != 0
	h.HeldAt = time.Unix(0, held).UTC()
	h.GraceUntil = nullNanosToTime(grace)
	return h, true, nil
}

func (s *Store) upsertHold(ctx context.Context, h RouteHold) error {
	var tokenID any
	if h.TokenID != "" {
		tokenID = h.TokenID
	}
	var grace any
	if h.GraceUntil != nil {
		grace = h.GraceUntil.UnixNano()
	}
	eph := 0
	if h.Ephemeral {
		eph = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO route_holds (host, token_id, kind, state, ephemeral, held_at, grace_until)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET
		   token_id = excluded.token_id, kind = excluded.kind, state = excluded.state,
		   ephemeral = excluded.ephemeral, held_at = excluded.held_at,
		   grace_until = excluded.grace_until`,
		h.Host, tokenID, string(h.Kind), h.State, eph, h.HeldAt.UnixNano(), grace); err != nil {
		return fmt.Errorf("upsert hold: %w", err)
	}
	return nil
}

func (s *Store) deleteHold(ctx context.Context, host string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM route_holds WHERE host = ?`, host); err != nil {
		return fmt.Errorf("delete hold: %w", err)
	}
	return nil
}

// holdBlocked reports whether an existing hold blocks req at time now.
//
//   - A namespace hold is session-only: any active holder blocks (released
//     namespace holds are deleted, so a found namespace hold is active).
//   - An active token hold blocks everyone but the same token (which resumes).
//   - A released token hold blocks others only while inside its grace window;
//     once grace elapses the host is free.
func holdBlocked(existing RouteHold, req HoldRequest, now time.Time) bool {
	if existing.Kind == HoldByNamespace {
		return existing.State == holdStateActive
	}

	sameToken := req.Kind == HoldByToken && req.TokenID != "" && existing.TokenID == req.TokenID
	if existing.State == holdStateActive {
		return !sameToken
	}

	inGrace := existing.GraceUntil != nil && now.Before(*existing.GraceUntil)
	if inGrace {
		return !sameToken
	}
	return false
}
