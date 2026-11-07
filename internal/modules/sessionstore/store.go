// Package sessionstore is ZeoKeel's SQLite-backed session store kernel module.
//
// It owns the durable half of a conversation: message history (Rig
// rig.History), interruption checkpoints (rig.CheckpointStore), the session
// event log (rig.SessionLog) and the product's session list metadata. The UI
// and the agent backend consume it through contracts.KeyStore; the database
// lives in the product data directory (contracts.KeyDataDir).
//
// The schema is created idempotently on open:
//
//	sessions(id TEXT PRIMARY KEY, title TEXT, created_at INTEGER, updated_at INTEGER)
//	messages(id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL
//	         REFERENCES sessions(id) ON DELETE CASCADE, ordinal INTEGER,
//	         payload BLOB NOT NULL, created_at INTEGER)
//	events(seq INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, type TEXT,
//	       payload BLOB, created_at INTEGER)
//	checkpoints(session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
//	            payload BLOB, updated_at INTEGER)
//
// The database uses WAL, foreign keys and a busy timeout, all applied on every
// connection through the driver DSN. Timestamps are stored as UTC unix
// nanoseconds; message payloads are JSON-encoded rig.Message values, checkpoint
// payloads are JSON-encoded rig.Checkpoint values, event payloads are stored
// verbatim as JSON.
//
// # Semantics worth knowing
//
//   - Append (history) and AppendEvent create the session row when it is
//     missing and bump the session's updated_at timestamp; they are atomic
//     transactions. A second Store opened on the same file serializes through
//     SQLite's busy timeout instead of returning SQLITE_BUSY.
//   - History.Load on an unknown session returns (nil, nil); CheckpointStore
//     Load returns (nil, nil) when no checkpoint was saved; Last with a
//     non-positive limit and Events for an unknown session return an empty
//     slice.
//   - Search matches the event type and payload text case-insensitively with
//     LIKE wildcards in the query treated literally. An empty query returns all
//     events; a non-positive limit means no limit (callers pass a positive
//     limit for bounded UI queries).
//   - DeleteSession removes the session's messages, events and checkpoint
//     explicitly (events has no foreign key in the fixed schema); deleting an
//     unknown session is a no-op. RenameSession on an unknown session returns
//     ErrUnknownSession.
package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/sessionstore"

	// DBFileName is the database file inside the product data directory.
	DBFileName = "zeokeel.db"

	// DefaultBusyTimeout is used when Config.BusyTimeoutMs is zero.
	DefaultBusyTimeout = 5 * time.Second
)

// ErrUnknownSession is returned by operations that require an existing session
// (for example RenameSession).
var ErrUnknownSession = errors.New("sessionstore: unknown session")

// Config is the module's typed assembly config.
type Config struct {
	// Path overrides the database location. An empty value uses
	// <dataDir>/zeokeel.db; a relative value is resolved against the data
	// directory.
	Path string `json:"path,omitempty"`
	// BusyTimeoutMs is SQLite's busy timeout for lock contention (0 = 5000ms).
	BusyTimeoutMs int `json:"busyTimeoutMs,omitempty"`
}

// storeOptions is the resolved construction input for one database.
type storeOptions struct {
	dataDir     string
	path        string
	busyTimeout time.Duration
	logger      contracts.Logger
}

// store is the open SQLite database and its contracts.Store implementation.
type store struct {
	db     *sql.DB
	path   string
	logger contracts.Logger
}

var _ contracts.Store = (*store)(nil)

// historyView is the rig.History view of a store.
type historyView struct{ store *store }

var _ rig.History = historyView{}

// checkpointView is the rig.CheckpointStore view of a store.
type checkpointView struct{ store *store }

var _ rig.CheckpointStore = checkpointView{}

// schemaStatements is the idempotent schema applied on every open.
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		title TEXT,
		created_at INTEGER,
		updated_at INTEGER
	)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		ordinal INTEGER,
		payload BLOB NOT NULL,
		created_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_session_ordinal ON messages(session_id, ordinal)`,
	`CREATE TABLE IF NOT EXISTS events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		type TEXT,
		payload BLOB,
		created_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, seq)`,
	`CREATE TABLE IF NOT EXISTS checkpoints (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		payload BLOB,
		updated_at INTEGER
	)`,
}

// openStore opens (and, when needed, creates) the SQLite database.
func openStore(opts storeOptions) (*store, error) {
	path, err := resolveDatabasePath(opts.dataDir, opts.path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("sessionstore: create data directory: %w", err)
	}
	busyTimeout := opts.busyTimeout
	if busyTimeout <= 0 {
		busyTimeout = DefaultBusyTimeout
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("sessionstore: open %q: %w", path, err)
	}
	// SQLite serializes writers anyway; one connection per store keeps the
	// per-connection PRAGMAs (foreign keys, busy timeout) stable and makes the
	// schema/transaction code easy to reason about. Concurrent callers queue on
	// the pool instead of racing for locks.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sessionstore: ping %q: %w", path, err)
	}
	current := &store{db: db, path: path, logger: opts.logger}
	if err := current.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if current.logger != nil {
		current.logger.Printf("%s: opened database %s", ModuleID, path)
	}
	return current, nil
}

// resolveDatabasePath applies the default/relative-path rules.
func resolveDatabasePath(dataDir, override string) (string, error) {
	dataDir = strings.TrimSpace(dataDir)
	override = strings.TrimSpace(override)
	switch {
	case override == "":
		if dataDir == "" {
			return "", errors.New("sessionstore: data directory is required")
		}
		return filepath.Join(dataDir, DBFileName), nil
	case filepath.IsAbs(override):
		return filepath.Clean(override), nil
	case dataDir != "":
		return filepath.Join(dataDir, override), nil
	default:
		return filepath.Clean(override), nil
	}
}

// sqliteDSN builds the driver DSN: the database path plus the PRAGMAs applied
// to every connection (WAL, foreign keys, busy timeout).
func sqliteDSN(path string, busyTimeout time.Duration) string {
	query := url.Values{}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(1)")
	return path + "?" + query.Encode()
}

// initSchema creates every table/index (idempotent).
func (s *store) initSchema(ctx context.Context) error {
	for _, statement := range schemaStatements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sessionstore: initialize schema: %w", err)
		}
	}
	return nil
}

// Close closes the database. It is called by the module cleanup effect.
func (s *store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("sessionstore: close database: %w", err)
	}
	if s.logger != nil {
		s.logger.Printf("%s: closed database %s", ModuleID, s.path)
	}
	return nil
}

// --- Store: views ----------------------------------------------------------

// History implements contracts.Store.
func (s *store) History() rig.History { return historyView{store: s} }

// CheckpointStore implements contracts.Store.
func (s *store) CheckpointStore() rig.CheckpointStore { return checkpointView{store: s} }

// Load implements rig.History.
func (v historyView) Load(ctx context.Context, sessionID string) ([]rig.Message, error) {
	return v.store.loadMessages(ctx, sessionID)
}

// Append implements rig.History.
func (v historyView) Append(ctx context.Context, sessionID string, messages ...rig.Message) error {
	return v.store.appendMessages(ctx, sessionID, messages)
}

// Load implements rig.CheckpointStore.
func (v checkpointView) Load(ctx context.Context, sessionID string) (*rig.Checkpoint, error) {
	return v.store.loadCheckpoint(ctx, sessionID)
}

// Save implements rig.CheckpointStore.
func (v checkpointView) Save(ctx context.Context, sessionID string, checkpoint *rig.Checkpoint) error {
	return v.store.saveCheckpoint(ctx, sessionID, checkpoint)
}

// Clear implements rig.CheckpointStore.
func (v checkpointView) Clear(ctx context.Context, sessionID string) error {
	return v.store.clearCheckpoint(ctx, sessionID)
}

// --- Store: session metadata ----------------------------------------------

// CreateSession implements contracts.Store. Creating an existing session is a
// no-op: the title and timestamps are left untouched.
func (s *store) CreateSession(ctx context.Context, id, title string) error {
	id, err := requireSessionID(id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := nowNanos()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(id, title, created_at, updated_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		id, title, now, now); err != nil {
		return fmt.Errorf("sessionstore: create session %q: %w", id, err)
	}
	return nil
}

// DeleteSession implements contracts.Store. It removes the session, its
// messages, events and checkpoint in one transaction; an unknown session is a
// no-op.
func (s *store) DeleteSession(ctx context.Context, id string) error {
	id, err := requireSessionID(id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: delete session %q: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	// events has no foreign key in the fixed schema, so delete every dependent
	// row explicitly; the others are also explicit so deletion does not depend
	// on the foreign_keys pragma being enabled.
	for _, statement := range []string{
		`DELETE FROM events WHERE session_id = ?`,
		`DELETE FROM messages WHERE session_id = ?`,
		`DELETE FROM checkpoints WHERE session_id = ?`,
		`DELETE FROM sessions WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("sessionstore: delete session %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: delete session %q: %w", id, err)
	}
	return nil
}

// RenameSession implements contracts.Store. Renaming an unknown session returns
// ErrUnknownSession.
func (s *store) RenameSession(ctx context.Context, id, title string) error {
	id, err := requireSessionID(id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		title, nowNanos(), id)
	if err != nil {
		return fmt.Errorf("sessionstore: rename session %q: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sessionstore: rename session %q: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrUnknownSession, id)
	}
	return nil
}

// Sessions implements contracts.Store: newest updated first.
func (s *store) Sessions(ctx context.Context) ([]contracts.SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title, s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		FROM sessions s
		ORDER BY s.updated_at DESC, s.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list sessions: %w", err)
	}
	defer rows.Close()
	var metas []contracts.SessionMeta
	for rows.Next() {
		meta, err := scanSessionMeta(rows)
		if err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: list sessions: %w", err)
	}
	return metas, nil
}

// GetSessionMeta implements contracts.Store.
func (s *store) GetSessionMeta(ctx context.Context, id string) (contracts.SessionMeta, bool, error) {
	id, err := requireSessionID(id)
	if err != nil {
		return contracts.SessionMeta{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return contracts.SessionMeta{}, false, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.title, s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		FROM sessions s WHERE s.id = ?`, id)
	meta, err := scanSessionMetaRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.SessionMeta{}, false, nil
	}
	if err != nil {
		return contracts.SessionMeta{}, false, err
	}
	return meta, true, nil
}

// --- Store: history --------------------------------------------------------

// loadMessages returns a session's messages ordered by ordinal.
func (s *store) loadMessages(ctx context.Context, sessionID string) ([]rig.Message, error) {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT payload FROM messages WHERE session_id = ? ORDER BY ordinal ASC, id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: load history %q: %w", id, err)
	}
	defer rows.Close()
	var messages []rig.Message
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("sessionstore: load history %q: %w", id, err)
		}
		var message rig.Message
		if err := json.Unmarshal(payload, &message); err != nil {
			return nil, fmt.Errorf("sessionstore: decode message of session %q: %w", id, err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: load history %q: %w", id, err)
	}
	return messages, nil
}

// appendMessages appends messages atomically, creating the session row when
// needed and updating its updated_at timestamp.
func (s *store) appendMessages(ctx context.Context, sessionID string, messages []rig.Message) error {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return ctx.Err()
	}
	payloads := make([][]byte, 0, len(messages))
	for index, message := range messages {
		payload, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("sessionstore: encode message %d of session %q: %w", index, id, err)
		}
		payloads = append(payloads, payload)
	}
	now := nowNanos()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: append history %q: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSessionTx(ctx, tx, id, now); err != nil {
		return err
	}
	var baseOrdinal int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal), 0) FROM messages WHERE session_id = ?`, id).
		Scan(&baseOrdinal); err != nil {
		return fmt.Errorf("sessionstore: append history %q: %w", id, err)
	}
	for index, payload := range payloads {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO messages(session_id, ordinal, payload, created_at) VALUES(?, ?, ?, ?)`,
			id, baseOrdinal+int64(index)+1, payload, now); err != nil {
			return fmt.Errorf("sessionstore: append history %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: append history %q: %w", id, err)
	}
	return nil
}

// ReplaceHistory implements contracts.Store (Rig HistoryReplacer semantics).
func (s *store) ReplaceHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return err
	}
	payloads := make([][]byte, 0, len(messages))
	for index, message := range messages {
		payload, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("sessionstore: encode message %d of session %q: %w", index, id, err)
		}
		payloads = append(payloads, payload)
	}
	now := nowNanos()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: replace history %q: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSessionTx(ctx, tx, id, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("sessionstore: replace history %q: %w", id, err)
	}
	for index, payload := range payloads {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO messages(session_id, ordinal, payload, created_at) VALUES(?, ?, ?, ?)`,
			id, int64(index)+1, payload, now); err != nil {
			return fmt.Errorf("sessionstore: replace history %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: replace history %q: %w", id, err)
	}
	return nil
}

// --- Store: checkpoints ----------------------------------------------------

// loadCheckpoint returns (nil, nil) when the session has no checkpoint.
func (s *store) loadCheckpoint(ctx context.Context, sessionID string) (*rig.Checkpoint, error) {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var payload []byte
	err = s.db.QueryRowContext(ctx,
		`SELECT payload FROM checkpoints WHERE session_id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sessionstore: load checkpoint %q: %w", id, err)
	}
	var checkpoint rig.Checkpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return nil, fmt.Errorf("sessionstore: decode checkpoint %q: %w", id, err)
	}
	return &checkpoint, nil
}

// saveCheckpoint upserts the session checkpoint.
func (s *store) saveCheckpoint(ctx context.Context, sessionID string, checkpoint *rig.Checkpoint) error {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return err
	}
	if checkpoint == nil {
		return errors.New("sessionstore: checkpoint is required")
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("sessionstore: encode checkpoint %q: %w", id, err)
	}
	now := nowNanos()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: save checkpoint %q: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSessionTx(ctx, tx, id, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO checkpoints(session_id, payload, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		id, payload, now); err != nil {
		return fmt.Errorf("sessionstore: save checkpoint %q: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: save checkpoint %q: %w", id, err)
	}
	return nil
}

// clearCheckpoint removes a checkpoint (idempotent).
func (s *store) clearCheckpoint(ctx context.Context, sessionID string) error {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM checkpoints WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("sessionstore: clear checkpoint %q: %w", id, err)
	}
	return nil
}

// --- Store: session log ----------------------------------------------------

// AppendEvent implements contracts.Store: it records one event, creating the
// session row when needed and bumping its updated_at timestamp.
func (s *store) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	id, err := requireSessionID(event.SessionID)
	if err != nil {
		return err
	}
	now := nowNanos()
	createdAt := now
	if !event.Time.IsZero() {
		createdAt = event.Time.UTC().UnixNano()
	}
	var payload []byte
	if len(event.Payload) > 0 {
		payload = append([]byte(nil), event.Payload...)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: append event: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensureSessionTx(ctx, tx, id, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events(session_id, type, payload, created_at) VALUES(?, ?, ?, ?)`,
		id, event.Type, payload, createdAt); err != nil {
		return fmt.Errorf("sessionstore: append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: append event: %w", err)
	}
	return nil
}

// Events implements rig.SessionLog: from <= seq <= to, with to <= 0 meaning
// "until the end".
func (s *store) Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error) {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if from < 0 {
		from = 0
	}
	query := `SELECT seq, session_id, type, payload, created_at FROM events WHERE session_id = ? AND seq >= ?`
	args := []any{id, from}
	if to > 0 {
		query += ` AND seq <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY seq ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list events of %q: %w", id, err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// Search implements rig.SessionLog: a case-insensitive substring match over the
// event type and the JSON payload text. An empty query returns all events (up
// to limit); a non-positive limit means no limit.
func (s *store) Search(ctx context.Context, query string, limit int) ([]rig.SessionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(query)
	statement := `SELECT seq, session_id, type, payload, created_at FROM events`
	var args []any
	if trimmed != "" {
		pattern := "%" + escapeLike(trimmed) + "%"
		statement += ` WHERE (type LIKE ? ESCAPE '\' OR CAST(payload AS TEXT) LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}
	statement += ` ORDER BY seq ASC`
	if limit > 0 {
		statement += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: search events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// Last implements rig.SessionLog: the newest limit events of one session,
// returned in ascending sequence order.
func (s *store) Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	id, err := requireSessionID(sessionID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []rig.SessionEvent{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, session_id, type, payload, created_at FROM events
		 WHERE session_id = ? ORDER BY seq DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: last events of %q: %w", id, err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	// The query returned the newest rows in descending order; reverse the slice
	// so Last is the newest-limit window in chronological order.
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

// --- helpers ---------------------------------------------------------------

// execer is the subset of *sql.DB and *sql.Tx used by ensureSessionTx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ensureSessionTx creates the session row when missing and bumps updated_at.
// It is the first write of every transaction, so the SQLite write lock is taken
// before any read-max-ordinal query (concurrent appends stay serialized).
func ensureSessionTx(ctx context.Context, tx execer, id string, now int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(id, title, created_at, updated_at) VALUES(?, '', ?, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at`,
		id, now, now); err != nil {
		return fmt.Errorf("sessionstore: ensure session %q: %w", id, err)
	}
	return nil
}

// scanSessionMeta scans one session row from rows.
func scanSessionMeta(rows *sql.Rows) (contracts.SessionMeta, error) {
	var (
		meta    contracts.SessionMeta
		title   sql.NullString
		created sql.NullInt64
		updated sql.NullInt64
		count   int64
	)
	if err := rows.Scan(&meta.ID, &title, &created, &updated, &count); err != nil {
		return contracts.SessionMeta{}, fmt.Errorf("sessionstore: scan session: %w", err)
	}
	meta.Title = title.String
	meta.CreatedAt = timeFromNanos(created.Int64)
	meta.UpdatedAt = timeFromNanos(updated.Int64)
	meta.MessageCount = int(count)
	return meta, nil
}

// scanSessionMetaRow is scanSessionMeta for a single *sql.Row.
func scanSessionMetaRow(row *sql.Row) (contracts.SessionMeta, error) {
	var (
		meta    contracts.SessionMeta
		title   sql.NullString
		created sql.NullInt64
		updated sql.NullInt64
		count   int64
	)
	if err := row.Scan(&meta.ID, &title, &created, &updated, &count); err != nil {
		return contracts.SessionMeta{}, err
	}
	meta.Title = title.String
	meta.CreatedAt = timeFromNanos(created.Int64)
	meta.UpdatedAt = timeFromNanos(updated.Int64)
	meta.MessageCount = int(count)
	return meta, nil
}

// scanEvents scans an event result set.
func scanEvents(rows *sql.Rows) ([]rig.SessionEvent, error) {
	var events []rig.SessionEvent
	for rows.Next() {
		var (
			event   rig.SessionEvent
			session sql.NullString
			kind    sql.NullString
			payload []byte
			created sql.NullInt64
		)
		if err := rows.Scan(&event.Seq, &session, &kind, &payload, &created); err != nil {
			return nil, fmt.Errorf("sessionstore: scan event: %w", err)
		}
		event.SessionID = session.String
		event.Type = kind.String
		if len(payload) > 0 {
			event.Payload = append(json.RawMessage(nil), payload...)
		}
		if created.Valid {
			event.Time = timeFromNanos(created.Int64)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: scan events: %w", err)
	}
	return events, nil
}

// escapeLike escapes LIKE wildcards in a user-supplied search string.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// requireSessionID trims and validates one session ID argument.
func requireSessionID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("sessionstore: session id is required")
	}
	return id, nil
}

// nowNanos is the current UTC time in unix nanoseconds.
func nowNanos() int64 { return time.Now().UTC().UnixNano() }

// timeFromNanos converts stored unix nanoseconds to a UTC time (zero when
// unset).
func timeFromNanos(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}
