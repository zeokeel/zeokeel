// Package store is ZeoKeel's persistence layer: the SQLite index and the
// per-session JSONL transcript described in docs/PLAN.md §9.
//
// The index lives at <dir>/index.sqlite. It is opened with a single writer
// connection (SetMaxOpenConns(1)) and WAL journaling so that a crash leaves a
// recoverable database instead of a corrupted one. Every connection also
// enables foreign keys and a busy timeout.
//
// Schema upgrades are guarded by the schema_version table and applied by the
// ordered migration steps in migrations.go (each in its own transaction, with a
// VACUUM INTO backup before the first step and a schema_migrations log row per
// step). A brand-new database is created at v1 and upgraded through the same
// steps. A database written by a newer (or unsupported legacy) schema is
// rejected with ErrSchemaTooNew or ErrSchemaTooOld instead of being silently
// mutated, per docs/PLAN.md §9.4.
//
// All errors returned by this package are wrapped with context; use
// errors.Is/errors.As against the sentinel errors below to classify them.
package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

// SchemaVersion is the SQLite schema version this build reads and writes. It
// advances by exactly one per ordered migration in schemaMigrations.
const SchemaVersion = 2

// Sentinel errors. Callers classify failures with errors.Is.
var (
	// ErrNotFound reports a missing row (session, workspace, setting, ...).
	ErrNotFound = errors.New("store: not found")
	// ErrSchemaTooOld reports an index written with a legacy schema version.
	ErrSchemaTooOld = errors.New("store: schema version is older than supported")
	// ErrSchemaTooNew reports an index written by a newer ZeoKeel build.
	ErrSchemaTooNew = errors.New("store: schema version is newer than supported")
	// ErrInvalidArgument reports invalid input such as an empty id or a
	// non-JSON event payload.
	ErrInvalidArgument = errors.New("store: invalid argument")
)

const (
	// dbFileName is the index file inside the store directory.
	dbFileName = "index.sqlite"
	// busyTimeoutMS is how long SQLite waits for a lock before failing.
	busyTimeoutMS = 5000
	// dirPerm is the mode used for the store and per-session directories.
	dirPerm = 0o700
	// filePerm is the mode enforced on the index and its WAL sidecars. The
	// index stores provider API keys and the full session history, so it must
	// not be world- or group-readable even when the directory mode was
	// loosened by an older build, a restore or a sync tool (RT-11).
	filePerm = 0o600
)

// Store is a handle to one on-disk ZeoKeel data directory.
//
// It is safe for concurrent use: database/sql serializes access through the
// single writer connection, but methods return once their transaction has
// committed.
type Store struct {
	dir  string
	path string
	db   *sql.DB
}

// Open opens (creating when needed) the index at <dir>/index.sqlite, applies
// the connection pragmas and migrates a fresh database to SchemaVersion.
//
// Open returns an error wrapping ErrSchemaTooOld or ErrSchemaTooNew when the
// existing index was written by an incompatible schema version; the file is
// left untouched in that case.
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("%w: open: empty directory", ErrInvalidArgument)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("store: open %q: create directory: %w", dir, err)
	}
	path := filepath.Join(dir, dbFileName)
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	// A single connection is the writer: it makes transaction ordering
	// deterministic and matches the "single-writer" model in docs/PLAN.md §9.2.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := db.Ping(); err != nil {
		db.Close() //nolint:errcheck // original error wins
		return nil, fmt.Errorf("store: open %q: connect: %w", path, err)
	}
	// Enforce the modes right after the database exists: SQLite copies the
	// database file's mode onto the WAL/-shm sidecars it creates later, so
	// securing index.sqlite before the first write keeps them 0600 too.
	if err := secureDatabaseFiles(dir, path); err != nil {
		db.Close() //nolint:errcheck // original error wins
		return nil, fmt.Errorf("store: open %q: %w", dir, err)
	}
	if err := applyPragmas(db); err != nil {
		db.Close() //nolint:errcheck // original error wins
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	if err := migrate(db, dir); err != nil {
		db.Close() //nolint:errcheck // original error wins
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	// A WAL sidecar may have been created before the mode above (or by a
	// previous process); re-check after migration so a repaired database and
	// its sidecars all end up 0600 while the connection is open.
	if err := secureDatabaseFiles(dir, path); err != nil {
		db.Close() //nolint:errcheck // original error wins
		return nil, fmt.Errorf("store: open %q: %w", dir, err)
	}
	return &Store{dir: dir, path: path, db: db}, nil
}

// secureDatabaseFiles enforces the store directory's 0700 and the index's (and
// any existing -wal/-shm sidecars') 0600 modes. The sidecars are optional: they
// only exist while WAL has pending pages, so a missing one is not an error.
func secureDatabaseFiles(dir, path string) error {
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("secure data directory: %w", err)
	}
	for _, name := range []string{dbFileName, dbFileName + "-wal", dbFileName + "-shm"} {
		target := filepath.Join(dir, name)
		if err := os.Chmod(target, filePerm); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("secure %s: %w", name, err)
		}
	}
	return nil
}

// Close releases the database connection. It is safe to call more than once.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close %q: %w", s.path, err)
	}
	return nil
}

// Dir returns the data directory this store was opened with.
func (s *Store) Dir() string { return s.dir }

// dsn builds the SQLite URI for path. Paths are made absolute and converted to
// an RFC 3986 file URI so that directories containing spaces, '?', '#' or '%'
// open correctly. The pragmas are also repeated on every new connection;
// _txlock=immediate takes the write lock up front, which suits a single-writer
// store and avoids lock upgrades failing mid-transaction.
func dsn(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive paths: C:/x -> /C:/x in the URI.
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}

// applyPragmas sets and verifies the connection pragmas required by §9.2.
func applyPragmas(db *sql.DB) error {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("set journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("set journal_mode: got %q, want wal", mode)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("set foreign_keys: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("set foreign_keys: got %d, want 1", foreignKeys)
	}

	if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMS)); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("read busy_timeout: %w", err)
	}
	if busyTimeout != busyTimeoutMS {
		return fmt.Errorf("set busy_timeout: got %d, want %d", busyTimeout, busyTimeoutMS)
	}
	return nil
}

// newID returns a random RFC 4122 version 4 UUID string.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// nowUTC is the single clock used for persisted timestamps.
func nowUTC() time.Time { return time.Now().UTC() }

// encodeTime renders t for a TEXT timestamp column: UTC RFC 3339 with
// nanoseconds, which sorts chronologically in SQLite's default collation.
func encodeTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// decodeTime parses a value written by encodeTime.
func decodeTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}
