package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// latestSchemaVersion is the version this round must reach. Kept literal so the
// test fails (rather than following) if the constant is not bumped.
const latestSchemaVersion = 2

// legacyMessage is one row of the schema-version-1 messages fixture.
type legacyMessage struct {
	id      string
	content string
	created string
}

// openLegacyV1 builds a real schema-version-1 index without ordinal columns,
// like a database written by an earlier ZeoKeel build. Messages are inserted in
// slice order, so their legacy read order (created_at, rowid) is deterministic.
func openLegacyV1(t *testing.T, dir string) *sql.DB {
	t.Helper()
	return openLegacyV1WithMessages(t, dir, []legacyMessage{
		{id: "ma", content: "msg a", created: "2026-01-01T00:00:03Z"},
		{id: "mb", content: "msg b", created: "2026-01-01T00:00:01Z"},
		{id: "mc", content: "msg c", created: "2026-01-01T00:00:02Z"},
	})
}

func openLegacyV1WithMessages(t *testing.T, dir string, messages []legacyMessage) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatalf("mkdir legacy store: %v", err)
	}
	path := filepath.Join(dir, dbFileName)
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range schemaV1 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy schema stmt failed: %v\n%s", err, stmt)
		}
	}
	for _, stmt := range schemaV1Additions {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy additions stmt failed: %v\n%s", err, stmt)
		}
	}
	now := nowUTCString()
	if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (1)`); err != nil {
		t.Fatalf("record legacy version: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workspaces(id, name, root, created_at) VALUES ('w1', 'legacy', '/tmp/legacy', ?)`, now,
	); err != nil {
		t.Fatalf("insert legacy workspace: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions(id, workspace_id, title, created_at, updated_at, status)
		 VALUES ('s1', 'w1', 'legacy session', ?, ?, 'idle')`, now, now,
	); err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
	for _, msg := range messages {
		if _, err := db.Exec(
			`INSERT INTO messages(id, session_id, role, content, reasoning, created_at, turn_id)
			 VALUES (?, 's1', 'user', ?, '', ?, '')`,
			msg.id, msg.content, msg.created,
		); err != nil {
			t.Fatalf("insert legacy message %q: %v", msg.id, err)
		}
	}
	return db
}

// TestLegacyV1UpgradePreservesSameTimestampTieBreak pins the ordinal backfill's
// tie-break: legacy v1 ListMessages read `ORDER BY created_at, rowid`, so two
// messages with the same created_at display in insertion order. The migration
// must reproduce that order, not fall back to the uuid id order (verifier-core
// Round3 AR-07 probe used zzz-message inserted before aaa-message).
func TestLegacyV1UpgradePreservesSameTimestampTieBreak(t *testing.T) {
	dir := t.TempDir()
	const sameInstant = "2026-01-01T00:00:01Z"
	legacy := openLegacyV1WithMessages(t, dir, []legacyMessage{
		{id: "zzz-message", content: "inserted first", created: sameInstant},
		{id: "aaa-message", content: "inserted second", created: sameInstant},
	})
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(legacy v1): %v", err)
	}
	defer func() { _ = s.Close() }()

	got, err := s.ListMessages("s1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2", len(got))
	}
	// Legacy v1 order: created_at equal, rowid = insertion order.
	want := []string{"zzz-message", "aaa-message"}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("upgraded order = [%s %s], want the legacy v1 order %v (tie-break must use rowid, not id)", got[0].ID, got[1].ID, want)
		}
		if got[i].Ordinal != int64(i+1) {
			t.Fatalf("message %q ordinal = %d, want %d", got[i].ID, got[i].Ordinal, i+1)
		}
	}
}

func nowUTCString() string {
	return encodeTime(nowUTC())
}

// TestFreshDatabaseReachesLatestVersionWithMigrationLog pins the AR-09
// framework: a fresh index records every applied migration in the ordered log
// table and reports the latest schema version.
func TestFreshDatabaseReachesLatestVersionWithMigrationLog(t *testing.T) {
	s := openTestStore(t)
	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion)
	}
	rows, err := s.db.Query(`SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	var (
		gotVersions []int
		names       []string
	)
	for rows.Next() {
		var (
			v    int
			name string
		)
		if err := rows.Scan(&v, &name); err != nil {
			t.Fatalf("scan migration row: %v", err)
		}
		gotVersions = append(gotVersions, v)
		names = append(names, name)
	}
	if len(gotVersions) == 0 || gotVersions[len(gotVersions)-1] != latestSchemaVersion {
		t.Fatalf("migration log versions = %v, want latest %d recorded", gotVersions, latestSchemaVersion)
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			t.Fatalf("migration log has an empty name: %v", names)
		}
	}
}

// TestLegacyV1UpgradesWithBackupAndOrdinalBackfill pins the upgrade path: an
// existing v1 index is migrated in order, backed up before the change, and the
// messages got their ordinal column backfilled from their old ordering.
func TestLegacyV1UpgradesWithBackupAndOrdinalBackfill(t *testing.T) {
	dir := t.TempDir()
	legacy := openLegacyV1(t, dir)
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(legacy v1): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("upgraded version = %d, want %d", version, latestSchemaVersion)
	}

	// A pre-migration backup must exist under <dir>/backups.
	backups, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("no pre-migration backup was written")
	}
	backup := filepath.Join(dir, "backups", backups[0].Name())
	if info, err := os.Stat(backup); err != nil || info.Size() == 0 {
		t.Fatalf("backup file %q missing or empty: %v", backup, err)
	}

	// The ordinal column was added and backfilled in the old sort order
	// (created_at, id), so message ids are ordered by their legacy timestamps.
	msgs, err := s.ListMessages("s1")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	want := []string{"mb", "mc", "ma"} // 00:00:01, 00:00:02, 00:00:03
	for i, msg := range msgs {
		if msg.ID != want[i] {
			t.Fatalf("message order = [%s %s %s], want %v", msgs[0].ID, msgs[1].ID, msgs[2].ID, want)
		}
		if msg.Ordinal != int64(i+1) {
			t.Fatalf("message %q ordinal = %d, want %d", msg.ID, msg.Ordinal, i+1)
		}
	}

	// Reopening an already-migrated index is idempotent: no new backup, no
	// duplicate migration log rows.
	before := len(backups)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	after, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("read backups after reopen: %v", err)
	}
	if len(after) != before {
		t.Fatalf("reopen wrote another backup (%d -> %d)", before, len(after))
	}
	var logCount int
	if err := again.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&logCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if logCount != beforeMigrationCount() {
		t.Fatalf("migration log rows after reopen = %d, want %d", logCount, beforeMigrationCount())
	}
}

// TestFailedMigrationRollsBackAndKeepsBackup pins fail-closed behaviour: a
// migration error leaves the old schema version in place, keeps the backup and
// makes Open fail instead of exposing a half-migrated database.
func TestFailedMigrationRollsBackAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	legacy := openLegacyV1(t, dir)
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	migrationFailureHook = func(version int) error {
		if version == 2 {
			return errMigrationInjected
		}
		return nil
	}
	t.Cleanup(func() { migrationFailureHook = nil })

	if _, err := Open(dir); err == nil {
		t.Fatal("Open with a failing migration = nil error")
	}

	// The database stayed at v1: reopening with the hook removed migrates it.
	migrationFailureHook = nil
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after failure: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	backups, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("failed migration did not keep a backup: %v (entries %d)", err, len(backups))
	}
	// The ordinal migration did not partially apply: version is only recorded
	// with the whole step.
	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("version after recovered open = %d, want %d", version, latestSchemaVersion)
	}
}

// beforeMigrationCount is how many ordered migration steps exist (>= one).
func beforeMigrationCount() int { return len(schemaMigrations) }

var errMigrationInjected = errInjected("migration failure injected by test")

type errInjected string

func (e errInjected) Error() string { return string(e) }
