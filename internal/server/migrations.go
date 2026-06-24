package server

import (
	"context"
	"fmt"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS tokens (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		token_hash  TEXT NOT NULL UNIQUE,
		created_at  INTEGER NOT NULL,
		revoked_at  INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS token_allow_patterns (
		token_id    TEXT NOT NULL,
		pattern     TEXT NOT NULL,
		FOREIGN KEY(token_id) REFERENCES tokens(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS route_holds (
		host        TEXT PRIMARY KEY,
		token_id    TEXT,
		kind        TEXT NOT NULL CHECK (kind IN ('token', 'namespace')),
		state       TEXT NOT NULL CHECK (state IN ('active', 'released')),
		ephemeral   INTEGER NOT NULL DEFAULT 0 CHECK (ephemeral IN (0, 1)),
		held_at     INTEGER NOT NULL,
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
