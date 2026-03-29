// Package sqlite provides a SQLite-backed implementation of the EventStore interface.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	// modernc.org/sqlite is a pure-Go SQLite driver (no CGO required).
	_ "modernc.org/sqlite"
)

// trackingSchema defines the DDL for tracking.db.
const trackingSchema = `
-- Core event table (append-only, high-write)
CREATE TABLE IF NOT EXISTS events (
    id                TEXT PRIMARY KEY,
    agent_id          TEXT NOT NULL,
    session_id        TEXT NOT NULL,
    event_type        TEXT NOT NULL,
    model             TEXT NOT NULL,
    timestamp         INTEGER NOT NULL,
    duration_ms       INTEGER,
    prompt_tokens     INTEGER,
    completion_tokens INTEGER,
    cost_usd          REAL,
    quality_score     REAL,
    rework_count      INTEGER DEFAULT 0,
    tool_name         TEXT,
    tool_success      INTEGER,
    metadata          TEXT
);

-- Indexes for query performance
CREATE INDEX IF NOT EXISTS idx_events_agent_ts ON events(agent_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_type_ts ON events(event_type, timestamp);
CREATE INDEX IF NOT EXISTS idx_events_model ON events(model, timestamp);

-- Materialized summary cache
CREATE TABLE IF NOT EXISTS agent_summaries (
    agent_id        TEXT PRIMARY KEY,
    last_event_ts   INTEGER,
    total_events    INTEGER DEFAULT 0,
    total_cost_usd  REAL DEFAULT 0.0,
    avg_quality     REAL DEFAULT 0.0,
    updated_at      INTEGER
);
`

// walPragmas configures SQLite for high-concurrency WAL mode.
// These are applied every time a connection is opened.
const walPragmas = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=NORMAL;
PRAGMA wal_autocheckpoint=1000;
`

// openDB opens (or creates) a SQLite database at path and applies WAL pragmas.
// The returned *sql.DB should be closed by the caller.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}

	// Single writer connection — we use the channel pattern for writes,
	// so a single open connection is sufficient and avoids locking issues.
	db.SetMaxOpenConns(1)

	if err := applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// openReadDB opens a read-only connection pool (WAL mode allows concurrent reads).
func openReadDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open sqlite read-only database %q: %w", path, err)
	}

	// Allow up to 10 concurrent read connections (WAL supports this).
	db.SetMaxOpenConns(10)

	if err := applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// applyPragmas executes the WAL configuration pragmas.
func applyPragmas(db *sql.DB) error {
	if _, err := db.Exec(walPragmas); err != nil {
		return fmt.Errorf("apply WAL pragmas: %w", err)
	}
	return nil
}

// ApplyTrackingMigrations creates all tables and indexes for tracking.db.
// It is idempotent (uses CREATE IF NOT EXISTS) and safe to call at startup.
func ApplyTrackingMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, trackingSchema); err != nil {
		return fmt.Errorf("apply tracking schema: %w", err)
	}
	return nil
}
