package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
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

const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// CreateToken mints a token: it generates the secret, Argon2id-hashes it, and
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
	hash, err := hashSecret(secret)
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
		`INSERT INTO tokens (id, name, argon2, created_at) VALUES (?, ?, ?, ?)`,
		id, name, hash, time.Now().UTC().UnixNano()); err != nil {
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
// It scans active tokens and Argon2id-verifies each: O(n) in the number of
// tokens, which is fine at operator scale.
func (s *Store) VerifyToken(ctx context.Context, secret string) (*Token, error) {
	if !strings.HasPrefix(secret, tokenPrefix) {
		return nil, ErrTokenInvalid
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, argon2, created_at FROM tokens WHERE revoked_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("query tokens: %w", err)
	}
	type candidate struct {
		id, name, hash string
		created        int64
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.name, &c.hash, &c.created); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan token: %w", err)
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	for _, c := range cands {
		ok, verr := verifySecret(secret, c.hash)
		if verr != nil {
			continue // skip a malformed stored hash rather than failing the whole verify
		}
		if ok {
			patterns, perr := s.loadPatterns(ctx, c.id)
			if perr != nil {
				return nil, perr
			}
			return &Token{
				ID:        c.id,
				Name:      c.name,
				Patterns:  patterns,
				CreatedAt: time.Unix(0, c.created).UTC(),
			}, nil
		}
	}
	return nil, ErrTokenInvalid
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

func hashSecret(secret string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifySecret(secret, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid argon2id hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse argon2 version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("incompatible argon2 version %d", version)
	}
	var (
		memory  uint32
		timeArg uint32
		threads uint8
	)
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeArg, &threads); err != nil {
		return false, fmt.Errorf("parse argon2 params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode key: %w", err)
	}
	got := argon2.IDKey([]byte(secret), salt, timeArg, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func nullNanosToTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(0, n.Int64).UTC()
	return &t
}
