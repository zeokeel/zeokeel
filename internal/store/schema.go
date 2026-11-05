package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// schemaV1 is the ordered DDL for schema version 1 (docs/PLAN.md §9.2).
// Statements are executed inside one transaction; CREATE (not IF NOT EXISTS)
// is intentional so a half-created index fails loudly instead of being
// silently accepted.
var schemaV1 = []string{
	`CREATE TABLE schema_version (
		version INTEGER NOT NULL
	)`,
	`CREATE TABLE workspaces (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		root       TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE sessions (
		id           TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
		title        TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL,
		status       TEXT NOT NULL
	)`,
	`CREATE INDEX sessions_workspace_idx ON sessions(workspace_id, updated_at DESC)`,
	`CREATE TABLE messages (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		role       TEXT NOT NULL,
		content    TEXT NOT NULL,
		reasoning  TEXT NOT NULL,
		created_at TEXT NOT NULL,
		turn_id    TEXT NOT NULL
	)`,
	`CREATE INDEX messages_session_idx ON messages(session_id, created_at, id)`,
	`CREATE TABLE tool_calls (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
		name       TEXT NOT NULL,
		args       TEXT NOT NULL,
		result     TEXT NOT NULL,
		status     TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at   TEXT NOT NULL,
		approval   TEXT NOT NULL
	)`,
	`CREATE INDEX tool_calls_session_idx ON tool_calls(session_id, started_at)`,
	`CREATE TABLE events (
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		seq        INTEGER NOT NULL,
		type       TEXT NOT NULL,
		payload    TEXT,
		ts         TEXT NOT NULL,
		PRIMARY KEY (session_id, seq)
	)`,
	`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE automations (
		id         TEXT PRIMARY KEY,
		title      TEXT NOT NULL,
		prompt     TEXT NOT NULL,
		cron       TEXT NOT NULL,
		enabled    INTEGER NOT NULL,
		last_run   TEXT,
		next_run   TEXT,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX automations_next_run_idx ON automations(enabled, next_run)`,
}

// schemaV1Additions are idempotent additions to schema version 1. They let an
// index created by an earlier M-stage build gain new tables without bumping
// SchemaVersion (which would reject every existing index as "too old"). A
// fresh database also runs them after schemaV1, so both paths converge.
var schemaV1Additions = []string{
	`CREATE TABLE IF NOT EXISTS automations (
		id         TEXT PRIMARY KEY,
		title      TEXT NOT NULL,
		prompt     TEXT NOT NULL,
		cron       TEXT NOT NULL,
		enabled    INTEGER NOT NULL,
		last_run   TEXT,
		next_run   TEXT,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS automations_next_run_idx ON automations(enabled, next_run)`,
	`CREATE TABLE IF NOT EXISTS checkpoints (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		history    TEXT NOT NULL,
		turn_id    TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		phase      TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS attachments (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
		name       TEXT NOT NULL,
		path       TEXT NOT NULL,
		mime       TEXT NOT NULL,
		kind       TEXT NOT NULL,
		size       INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS attachments_session_idx ON attachments(session_id, created_at)`,
	// Feedback is append-only: every thumbs action records a row, and the UI
	// reflects the newest rating for a message. A session-level marker uses
	// the synthetic message id "session".
	`CREATE TABLE IF NOT EXISTS feedback (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		message_id TEXT NOT NULL DEFAULT '',
		rating     TEXT NOT NULL,
		note       TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS feedback_session_idx ON feedback(session_id, created_at, id)`,
	// Memories are workspace-scoped notes that survive sessions and are
	// injected into future turns. workspace_id deliberately has no foreign
	// key: every read supplies an explicit scope, and this additive migration
	// must not change workspace-deletion semantics.
	`CREATE TABLE IF NOT EXISTS memories (
		id           TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		content      TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS memories_workspace_idx ON memories(workspace_id, updated_at DESC)`,
}

// migrate brings db to SchemaVersion or rejects an incompatible index.
//
// A missing schema_version table means a fresh database and is created at
// SchemaVersion. A table that exists is authoritative: older versions are
// rejected (docs/PLAN.md §9.4 "低版本拒绝启动并提示") and newer versions are
// rejected because this build cannot know their invariants.
func migrate(db *sql.DB) error {
	exists, err := tableExists(db, "schema_version")
	if err != nil {
		return err
	}
	if !exists {
		return createSchemaV1(db)
	}
	version, err := schemaVersion(db)
	if err != nil {
		return err
	}
	switch {
	case version == SchemaVersion:
		// An index created by an older build that also used SchemaVersion 1
		// may miss tables added since. The additions are idempotent.
		return ensureSchemaAdditions(db)
	case version < SchemaVersion:
		return fmt.Errorf("%w: found %d, this build needs %d; remove the index or restore a backup", ErrSchemaTooOld, version, SchemaVersion)
	default:
		return fmt.Errorf("%w: found %d, this build supports %d; upgrade ZeoKeel", ErrSchemaTooNew, version, SchemaVersion)
	}
}

// createSchemaV1 creates the v1 schema and records its version atomically.
func createSchemaV1(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	for _, stmt := range schemaV1 {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create schema v1: %w", err)
		}
	}
	// Additions are idempotent: a fresh database runs them too, so a new
	// table added in a later build reaches both fresh and upgraded indexes.
	for _, stmt := range schemaV1Additions {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create schema v1 additions: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, SchemaVersion); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

// ensureSchemaAdditions applies the idempotent schema additions. It runs for
// a database already at SchemaVersion, so an index from a build that shipped
// before a table existed gains it on the next Open.
func ensureSchemaAdditions(db *sql.DB) error {
	for _, stmt := range schemaV1Additions {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema addition: %w", err)
		}
	}
	return nil
}

// schemaVersion returns the highest recorded schema version, or 0 when the
// table exists but holds no row (a corrupt or interrupted migration).
func schemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow(`SELECT version FROM schema_version ORDER BY version DESC LIMIT 1`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// tableExists reports whether a table with name exists in the main schema.
func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check table %q: %w", name, err)
	}
	return n > 0, nil
}
