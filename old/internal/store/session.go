package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SessionStatus is a session state from the runtime state machine
// (docs/PLAN.md §7: idle → running → waiting_approval | waiting_user | paused
// → running → idle | failed).
type SessionStatus string

// Session states persisted in the sessions.status column.
const (
	SessionIdle            SessionStatus = "idle"
	SessionRunning         SessionStatus = "running"
	SessionWaitingApproval SessionStatus = "waiting_approval"
	SessionWaitingUser     SessionStatus = "waiting_user"
	SessionPaused          SessionStatus = "paused"
	SessionFailed          SessionStatus = "failed"
)

// Session is one conversation inside a workspace (docs/PLAN.md §9.1).
type Session struct {
	ID          string        `json:"id"`
	WorkspaceID string        `json:"workspace_id"`
	Title       string        `json:"title"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Status      SessionStatus `json:"status"`
	// Archived hides a session from the sidebar and from search unless the
	// user asks to see archived sessions.
	Archived bool `json:"archived"`
}

// SessionSort selects the order of a SessionQuery.
type SessionSort string

// SessionQuery orders.
const (
	// SessionSortUpdated lists the most recently updated first.
	SessionSortUpdated SessionSort = "updated"
	// SessionSortManual keeps sessions in the order they were created.
	SessionSortManual SessionSort = "manual"
)

// SessionQuery filters and orders a session list. Searching matches the
// title or any persisted message content with SQL LIKE.
type SessionQuery struct {
	WorkspaceID     string
	Search          string
	IncludeArchived bool
	Sort            SessionSort
}

const sessionColumns = `id, workspace_id, title, created_at, updated_at, status`

// archivedSessionColumns is sessionColumns plus the archive flag, for the
// queries the search and archive UI uses.
const archivedSessionColumns = `id, workspace_id, title, created_at, updated_at, status, archived`

// sessionArchiveReady records the stores whose sessions table already has
// the archived column, so QuerySessions does not open a write transaction
// on every frame.
var sessionArchiveReady sync.Map // map[*Store]bool

// ensureSessionArchive applies the additive archived-column migration on
// first use. The base schema predates archiving and lives outside this
// change's ownership, so the upgrade is idempotent and applied lazily
// instead of through a schema-version bump; a fresh and an existing index
// both end up with the same column.
func (s *Store) ensureSessionArchive() error {
	if _, ok := sessionArchiveReady.Load(s); ok {
		return nil
	}
	if err := s.addSessionArchiveColumn(); err != nil {
		return err
	}
	sessionArchiveReady.Store(s, true)
	return nil
}

// addSessionArchiveColumn adds sessions.archived when it is missing.
func (s *Store) addSessionArchiveColumn() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: migrate sessions archive: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	var n int
	if err := tx.QueryRow(
		`SELECT count(*) FROM pragma_table_info('sessions') WHERE name = 'archived'`,
	).Scan(&n); err != nil {
		return fmt.Errorf("store: migrate sessions archive: inspect: %w", err)
	}
	if n == 0 {
		if _, err := tx.Exec(
			`ALTER TABLE sessions ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`,
		); err != nil {
			return fmt.Errorf("store: migrate sessions archive: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migrate sessions archive: commit: %w", err)
	}
	return nil
}

// QuerySessions returns the sessions matching q. A search matches the title
// or any message content, case-insensitively for ASCII, with LIKE; archived
// sessions are hidden unless q.IncludeArchived is set.
func (s *Store) QuerySessions(q SessionQuery) ([]Session, error) {
	if err := s.ensureSessionArchive(); err != nil {
		return nil, err
	}

	var (
		b    strings.Builder
		args []any
	)
	b.WriteString(`SELECT ` + archivedSessionColumns + ` FROM sessions s`)
	conds := make([]string, 0, 3)
	if q.WorkspaceID != "" {
		conds = append(conds, `s.workspace_id = ?`)
		args = append(args, q.WorkspaceID)
	}
	if !q.IncludeArchived {
		conds = append(conds, `s.archived = 0`)
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		pattern := likePattern(search)
		conds = append(conds, `(s.title LIKE ? ESCAPE '\' OR EXISTS (`+
			`SELECT 1 FROM messages m WHERE m.session_id = s.id AND m.content LIKE ? ESCAPE '\'))`)
		args = append(args, pattern, pattern)
	}
	if len(conds) > 0 {
		b.WriteString(` WHERE `)
		b.WriteString(strings.Join(conds, ` AND `))
	}
	switch q.Sort {
	case SessionSortManual:
		// Manual order is the order sessions were created in.
		b.WriteString(` ORDER BY s.rowid`)
	default:
		b.WriteString(` ORDER BY s.updated_at DESC, s.created_at DESC, s.id`)
	}

	rows, err := s.db.Query(b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("store: query sessions: %w", err)
	}
	defer rows.Close()

	list := make([]Session, 0)
	for rows.Next() {
		sess, err := scanArchivedSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: query sessions: %w", err)
		}
		list = append(list, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query sessions: %w", err)
	}
	// Relevance ordering is applied on top of the LIKE-filtered set: the filter
	// keeps the documented literal-substring semantics, while the FTS5 index
	// (when available) puts title matches and best BM25 matches first. A
	// ranking failure leaves the prior updated-time order untouched.
	if search := strings.TrimSpace(q.Search); search != "" {
		s.orderSessionsByRelevance(list, search)
	}
	return list, nil
}

// SetSessionArchived archives or unarchives a session and returns the
// updated row. Archiving deliberately leaves updated_at alone so it does not
// reorder the last-updated list.
func (s *Store) SetSessionArchived(id string, archived bool) (*Session, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: archive session: empty id", ErrInvalidArgument)
	}
	if err := s.ensureSessionArchive(); err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: archive session %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`UPDATE sessions SET archived = ? WHERE id = ?`, archived, id)
	if err != nil {
		return nil, fmt.Errorf("store: archive session %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: archive session %q: %w", id, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("store: archive session %q: %w", id, ErrNotFound)
	}
	sess, err := scanArchivedSession(tx.QueryRow(
		`SELECT `+archivedSessionColumns+` FROM sessions WHERE id = ?`, id,
	))
	if err != nil {
		return nil, fmt.Errorf("store: archive session %q: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: archive session %q: commit: %w", id, err)
	}
	return sess, nil
}

// likePattern renders q as a LIKE pattern with %, _ and \ escaped so the
// user's text matches literally inside the surrounding wildcards.
func likePattern(q string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(q) + "%"
}

// CreateSession creates an idle session in workspaceID. The workspace must
// exist; foreign keys are enforced by the database.
func (s *Store) CreateSession(workspaceID, title string) (*Session, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("%w: create session: empty workspace id", ErrInvalidArgument)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	sess := &Session{
		ID:          id,
		WorkspaceID: workspaceID,
		Title:       title,
		CreatedAt:   now,
		UpdatedAt:   now,
		Status:      SessionIdle,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create session: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	// Check explicitly so a missing workspace reports ErrNotFound instead of
	// surfacing a raw foreign-key constraint error.
	ok, err := workspaceExists(tx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("store: create session: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("store: create session: workspace %q: %w", workspaceID, ErrNotFound)
	}
	if _, err := tx.Exec(
		`INSERT INTO sessions(id, workspace_id, title, created_at, updated_at, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.WorkspaceID, sess.Title,
		encodeTime(sess.CreatedAt), encodeTime(sess.UpdatedAt), string(sess.Status),
	); err != nil {
		return nil, fmt.Errorf("store: create session: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create session: commit: %w", err)
	}
	return sess, nil
}

// GetSession returns the session with id, or an error wrapping ErrNotFound.
func (s *Store) GetSession(id string) (*Session, error) {
	sess, err := scanSession(s.db.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get session %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get session %q: %w", id, err)
	}
	return sess, nil
}

// GetSessionWithArchive returns the session with id including its archive
// flag. GetSession keeps its historical projection, which predates the
// archived column; new callers that care about archiving use this method.
func (s *Store) GetSessionWithArchive(id string) (*Session, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: get session: empty id", ErrInvalidArgument)
	}
	if err := s.ensureSessionArchive(); err != nil {
		return nil, err
	}
	sess, err := scanArchivedSession(s.db.QueryRow(
		`SELECT `+archivedSessionColumns+` FROM sessions WHERE id = ?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get session %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get session %q: %w", id, err)
	}
	return sess, nil
}

// UpdateSessionStatus stores status for id and bumps updated_at. It returns
// the updated session.
func (s *Store) UpdateSessionStatus(id string, status SessionStatus) (*Session, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: update session status: empty session id", ErrInvalidArgument)
	}
	if status == "" {
		return nil, fmt.Errorf("%w: update session status: empty status", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: update session status: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(
		`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), encodeTime(nowUTC()), id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: update session status %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: update session status %q: %w", id, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("store: update session status %q: %w", id, ErrNotFound)
	}
	sess, err := scanSession(tx.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("store: update session status %q: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: update session status %q: commit: %w", id, err)
	}
	return sess, nil
}

// UpdateSessionTitle stores title for id and bumps updated_at. It returns the
// updated session.
func (s *Store) UpdateSessionTitle(id, title string) (*Session, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: update session title: empty session id", ErrInvalidArgument)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("%w: update session title: empty title", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: update session title: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(
		`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		title, encodeTime(nowUTC()), id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: update session title %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: update session title %q: %w", id, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("store: update session title %q: %w", id, ErrNotFound)
	}
	sess, err := scanSession(tx.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("store: update session title %q: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: update session title %q: commit: %w", id, err)
	}
	return sess, nil
}

// DeleteSession removes the session with id. Its messages, events, tool calls
// and attachments are removed by the ON DELETE CASCADE clauses. It returns an
// error wrapping ErrNotFound when no such session exists.
func (s *Store) DeleteSession(id string) error {
	if id == "" {
		return fmt.Errorf("%w: delete session: empty session id", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: delete session %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	// The checkpoints table also has ON DELETE CASCADE, but deleting it
	// explicitly keeps the data-safety contract true for indexes created
	// before the table gained its foreign key and makes the intent obvious.
	if _, err := tx.Exec(`DELETE FROM checkpoints WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("store: delete session %q: delete checkpoint: %w", id, err)
	}
	// The FTS search index stores message content outside the messages table
	// (its shadow tables do not cascade), so clear it explicitly: "delete
	// session" must remove the session's searchable content as well.
	if _, ok := sessionSearchReady.Load(s); ok {
		if _, err := tx.Exec(`DELETE FROM `+sessionSearchTable+` WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("store: delete session %q: delete search index rows: %w", id, err)
		}
	}

	res, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete session %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete session %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete session %q: %w", id, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete session %q: commit: %w", id, err)
	}
	// The transcript directory is outside SQLite, so the cascade cannot reach
	// it. Remove it here so "delete session" removes everything stored under
	// the session, as the controller's contract says.
	if err := s.RemoveSessionDir(id); err != nil {
		return err
	}
	// Attachment copies live in a sibling directory, not under the session
	// transcript directory, so the cascade cannot reach them either.
	if err := s.RemoveAttachmentDir(id); err != nil {
		return err
	}
	return nil
}

// ListSessions returns sessions ordered by most recently updated first. An
// empty workspaceID lists every session.
func (s *Store) ListSessions(workspaceID string) ([]Session, error) {
	query := `SELECT ` + sessionColumns + ` FROM sessions`
	args := make([]any, 0, 1)
	if workspaceID != "" {
		query += ` WHERE workspace_id = ?`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC, id`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()

	list := make([]Session, 0)
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list sessions: %w", err)
		}
		list = append(list, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	return list, nil
}

// sessionExists reports whether a session row with id exists.
func sessionExists(tx *sql.Tx, id string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM sessions WHERE id = ?`, id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check session %q: %w", id, err)
	}
	return n > 0, nil
}

// scanArchivedSession reads the archivedSessionColumns projection.
func scanArchivedSession(r scanner) (*Session, error) {
	var (
		sess     Session
		created  string
		updated  string
		status   string
		archived int
	)
	if err := r.Scan(&sess.ID, &sess.WorkspaceID, &sess.Title, &created, &updated, &status, &archived); err != nil {
		return nil, err
	}
	var err error
	if sess.CreatedAt, err = decodeTime(created); err != nil {
		return nil, err
	}
	if sess.UpdatedAt, err = decodeTime(updated); err != nil {
		return nil, err
	}
	sess.Status = SessionStatus(status)
	sess.Archived = archived != 0
	return &sess, nil
}

// scanSession reads the sessionColumns projection.
func scanSession(r scanner) (*Session, error) {
	var (
		sess    Session
		created string
		updated string
		status  string
	)
	if err := r.Scan(&sess.ID, &sess.WorkspaceID, &sess.Title, &created, &updated, &status); err != nil {
		return nil, err
	}
	var err error
	if sess.CreatedAt, err = decodeTime(created); err != nil {
		return nil, err
	}
	if sess.UpdatedAt, err = decodeTime(updated); err != nil {
		return nil, err
	}
	sess.Status = SessionStatus(status)
	return &sess, nil
}
