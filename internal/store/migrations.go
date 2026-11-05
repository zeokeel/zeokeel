package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Ordered schema migrations (AR-09).
//
// SchemaVersion is the target version. Every step in schemaMigrations runs in
// its own transaction together with its schema_migrations log row and the
// schema_version update, so a failure leaves the database at the previous
// version (no half-applied step) while the pre-migration backup stays on disk.
// Before the first step of an upgrade the index is backed up with VACUUM INTO
// (a consistent snapshot that includes WAL content) under <dir>/backups/.
//
// A fresh database is created at v1 and immediately upgraded through the same
// ordered steps, so there is exactly one migration code path.
type schemaMigration struct {
	Version int
	Name    string
	Apply   func(tx *sql.Tx) error
}

// schemaMigrations is the ordered migration log. Append only; never edit an
// existing step (databases in the field already applied it).
var schemaMigrations = []schemaMigration{
	{
		Version: 2,
		Name:    "messages-ordinal",
		Apply:   migrateMessagesOrdinal,
	},
}

// migrationFailureHook is a test seam: it runs before one migration step and a
// non-nil error makes that step fail (used to pin rollback/backup behavior).
var migrationFailureHook func(version int) error

const backupsDirName = "backups"

// migrate brings db to SchemaVersion or rejects an incompatible index.
func migrate(db *sql.DB, dir string) error {
	exists, err := tableExists(db, "schema_version")
	if err != nil {
		return err
	}
	if !exists {
		if err := createSchemaV1(db); err != nil {
			return err
		}
		// Fresh indexes go through the ordered upgrades too; the backup is a
		// harmless empty snapshot and keeps one migration code path.
		return upgradeSchema(db, dir, 1)
	}
	version, err := schemaVersion(db)
	if err != nil {
		return err
	}
	switch {
	case version == SchemaVersion:
		// An index created by an older build that also reported SchemaVersion 1
		// may miss idempotent additions; keep applying them.
		return ensureSchemaAdditions(db)
	case version < 1:
		return fmt.Errorf("%w: found %d, this build needs %d; remove the index or restore a backup", ErrSchemaTooOld, version, SchemaVersion)
	case version > SchemaVersion:
		return fmt.Errorf("%w: found %d, this build supports %d; upgrade ZeoKeel", ErrSchemaTooNew, version, SchemaVersion)
	default:
		return upgradeSchema(db, dir, version)
	}
}

// upgradeSchema backs up the database and applies every ordered migration from
// version+1 to SchemaVersion, one transaction per step.
func upgradeSchema(db *sql.DB, dir string, from int) error {
	steps := make([]schemaMigration, 0, len(schemaMigrations))
	for _, step := range schemaMigrations {
		if step.Version > from && step.Version <= SchemaVersion {
			steps = append(steps, step)
		}
	}
	if len(steps) == 0 {
		return nil
	}
	if err := ensureMigrationLog(db); err != nil {
		return err
	}
	if err := backupDatabase(db, dir, from, SchemaVersion); err != nil {
		return fmt.Errorf("store: back up index before migration: %w", err)
	}
	for _, step := range steps {
		if migrationFailureHook != nil {
			if err := migrationFailureHook(step.Version); err != nil {
				return fmt.Errorf("store: migrate v%d (%s): %w", step.Version, step.Name, err)
			}
		}
		if err := applyMigrationStep(db, step); err != nil {
			return err
		}
	}
	return ensureSchemaAdditions(db)
}

// applyMigrationStep runs one ordered migration in a single transaction that also
// records the log row and advances schema_version.
func applyMigrationStep(db *sql.DB, step schemaMigration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: migrate v%d: begin: %w", step.Version, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if step.Apply != nil {
		if err := step.Apply(tx); err != nil {
			return fmt.Errorf("store: migrate v%d (%s): %w", step.Version, step.Name, err)
		}
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
		step.Version, step.Name, encodeTime(nowUTC()),
	); err != nil {
		return fmt.Errorf("store: migrate v%d: record log: %w", step.Version, err)
	}
	if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, step.Version); err != nil {
		return fmt.Errorf("store: migrate v%d: record version: %w", step.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migrate v%d: commit: %w", step.Version, err)
	}
	return nil
}

// ensureMigrationLog creates the ordered-migration log table when missing.
func ensureMigrationLog(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	return nil
}

// backupDatabase writes a consistent pre-migration snapshot under
// <dir>/backups/ using VACUUM INTO. The file is 0600 like the index. A backup
// failure aborts the migration (fail-closed) instead of changing the schema
// without a recovery point.
func backupDatabase(db *sql.DB, dir string, from, to int) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("store: migration backup: empty data directory")
	}
	backupDir := filepath.Join(dir, backupsDirName)
	if err := os.MkdirAll(backupDir, dirPerm); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	stamp := nowUTC().Format("20060102T150405.000000000Z")
	path := filepath.Join(backupDir, fmt.Sprintf("index-v%d-to-v%d-%s.sqlite", from, to, stamp))
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("vacuum into %q: %w", path, err)
	}
	if err := os.Chmod(path, filePerm); err != nil {
		return fmt.Errorf("secure backup %q: %w", path, err)
	}
	return nil
}

// migrateMessagesOrdinal is the AR-07 step: add a per-session ordinal column and
// backfill it from the historical ordering (created_at, id), so message order no
// longer depends on event replay ordering or timestamp uniqueness.
func migrateMessagesOrdinal(tx *sql.Tx) error {
	exists, err := columnExistsTx(tx, "messages", "ordinal")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add messages.ordinal: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE messages SET ordinal = (
			SELECT ranked.rn FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY session_id ORDER BY created_at, id
				) AS rn FROM messages
			) AS ranked WHERE ranked.id = messages.id
		)`); err != nil {
		return fmt.Errorf("backfill messages.ordinal: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS messages_session_ordinal_idx ON messages(session_id, ordinal)`); err != nil {
		return fmt.Errorf("index messages.ordinal: %w", err)
	}
	return nil
}

// columnExistsTx reports whether table has the named column.
func columnExistsTx(q schemaQuerier, table, column string) (bool, error) {
	rows, err := q.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}
