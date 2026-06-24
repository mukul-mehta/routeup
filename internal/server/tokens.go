package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const tokenPrefix = "sk_routeup_"

var ErrTokenInvalid = errors.New("token invalid or revoked")

type Token struct {
	ID        string
	Name      string
	Patterns  []AllowPattern
	CreatedAt time.Time
	RevokedAt *time.Time
}

func (t Token) Revoked() bool { return t.RevokedAt != nil }

// CreateToken mints a token: it generates the secret, SHA-256-hashes it, and
// stores the token plus its allow patterns in one transaction. It returns the
// token id (for list/revoke) and the plaintext secret, which is shown once and
// never recoverable.
func (s *Store) CreateToken(ctx context.Context, name string, patterns []AllowPattern) (id, secret string, err error) {
	if strings.TrimSpace(name) == "" {
		return "", "", errors.New("token name is required")
	}
	if len(patterns) == 0 {
		return "", "", errors.New("token needs at least one allow pattern")
	}

	secret, err = generateSecret()
	if err != nil {
		return "", "", err
	}
	id, err = generateTokenID()
	if err != nil {
		return "", "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO tokens (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		id, name, tokenHash(secret), time.Now().UTC().UnixNano()); err != nil {
		return "", "", fmt.Errorf("insert token: %w", err)
	}
	for _, p := range patterns {
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO token_allow_patterns (token_id, pattern) VALUES (?, ?)`,
			id, p.String()); err != nil {
			return "", "", fmt.Errorf("insert allow pattern: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return "", "", fmt.Errorf("commit token: %w", err)
	}
	return id, secret, nil
}

// VerifyToken returns the active token whose secret matches, or ErrTokenInvalid.
// Lookup is a single indexed query on the SHA-256 of the secret. No salt or KDF:
// the secret is 32 bytes of crypto/rand, so brute force is not the threat model.
func (s *Store) VerifyToken(ctx context.Context, secret string) (*Token, error) {
	if !strings.HasPrefix(secret, tokenPrefix) {
		return nil, ErrTokenInvalid
	}

	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM tokens WHERE token_hash = ? AND revoked_at IS NULL`,
		tokenHash(secret))
	var (
		id, name string
		created  int64
	)
	if err := row.Scan(&id, &name, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTokenInvalid
		}
		return nil, fmt.Errorf("query token: %w", err)
	}

	patterns, err := s.loadPatterns(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Token{
		ID:        id,
		Name:      name,
		Patterns:  patterns,
		CreatedAt: time.Unix(0, created).UTC(),
	}, nil
}

// ListTokens returns all tokens (active and revoked) with their patterns,
// ordered by creation time. Secrets are never returned.
func (s *Store) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at, revoked_at FROM tokens ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query tokens: %w", err)
	}
	var out []Token
	for rows.Next() {
		var (
			id, name string
			created  int64
			revoked  sql.NullInt64
		)
		if err := rows.Scan(&id, &name, &created, &revoked); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan token: %w", err)
		}
		out = append(out, Token{
			ID:        id,
			Name:      name,
			CreatedAt: time.Unix(0, created).UTC(),
			RevokedAt: nullNanosToTime(revoked),
		})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	for i := range out {
		patterns, err := s.loadPatterns(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Patterns = patterns
	}
	return out, nil
}

// RevokeToken marks the token revoked. It returns false when no active token
// with that id exists (already revoked or unknown).
func (s *Store) RevokeToken(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC().UnixNano(), id)
	if err != nil {
		return false, fmt.Errorf("revoke token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n > 0, nil
}

// TokenBases returns the distinct allow-pattern bases across all active
// (non-revoked) tokens, e.g. "routeup.dev" or "mukul.routeup.dev". Used to
// compute which namespace labels are reserved at the root tier.
func (s *Store) TokenBases(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT p.pattern FROM token_allow_patterns p
		 JOIN tokens t ON p.token_id = t.id
		 WHERE t.revoked_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("query token bases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seen := make(map[string]struct{})
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan pattern: %w", err)
		}
		p, err := ParseAllowPattern(raw)
		if err != nil {
			continue // skip a malformed stored pattern rather than failing
		}
		if _, ok := seen[p.Base()]; ok {
			continue
		}
		seen[p.Base()] = struct{}{}
		out = append(out, p.Base())
	}
	return out, rows.Err()
}

func (s *Store) loadPatterns(ctx context.Context, tokenID string) ([]AllowPattern, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pattern FROM token_allow_patterns WHERE token_id = ? ORDER BY pattern`, tokenID)
	if err != nil {
		return nil, fmt.Errorf("query patterns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AllowPattern
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan pattern: %w", err)
		}
		p, err := ParseAllowPattern(raw)
		if err != nil {
			return nil, fmt.Errorf("stored pattern %q: %w", raw, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token secret: %w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func generateTokenID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// tokenHash returns the hex-encoded SHA-256 of secret — the value stored in
// token_hash.
func tokenHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func nullNanosToTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(0, n.Int64).UTC()
	return &t
}
