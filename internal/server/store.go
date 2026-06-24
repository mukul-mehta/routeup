package server

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	claimMu sync.Mutex
}

func OpenStore(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS tokens (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		argon2      TEXT NOT NULL,
		created_at  INTEGER NOT NULL,
		revoked_at  INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS token_allow_patterns (
		token_id    TEXT NOT NULL,
		pattern     TEXT NOT NULL,
		FOREIGN KEY(token_id) REFERENCES tokens(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS claims (
		host        TEXT PRIMARY KEY,
		token_id    TEXT,
		kind        TEXT NOT NULL,
		state       TEXT NOT NULL,
		ephemeral   INTEGER NOT NULL DEFAULT 0,
		claimed_at  INTEGER NOT NULL,
		grace_until INTEGER
	)`,
}

func (s *Store) migrate(ctx context.Context) error {
	for i, stmt := range migrations {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %d: %w", i, err)
		}
	}
	return nil
}
