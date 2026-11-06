package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ForkSession copies a session and its durable history into a new session in
// the same workspace. The copy is titled "<title> (fork)" and starts idle and
// unarchived. Messages, events, tool calls, feedback, attachments and
// session-scoped settings are duplicated under fresh primary keys in one
// transaction, so a concurrent turn cannot observe a half-copied fork.
//
// Attachment rows are renumbered like messages and wired to the copied user
// message; their bytes are copied into the fork's own
// <data dir>/attachments/<fork id> directory so deleting either session cannot
// break the other's files.
//
// The source session is never modified. An unknown source is reported as
// ErrNotFound.
func (s *Store) ForkSession(sessionID string) (*Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: fork session: empty id", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: fork session %q: begin: %w", sessionID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	source, err := scanSession(tx.QueryRow(
		`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, sessionID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: fork session %q: read source: %w", sessionID, err)
	}

	newSessionID, err := newID()
	if err != nil {
		return nil, err
	}
	fork := &Session{
		ID:          newSessionID,
		WorkspaceID: source.WorkspaceID,
		Title:       source.Title + " (fork)",
		CreatedAt:   nowUTC(),
		UpdatedAt:   nowUTC(),
		Status:      SessionIdle,
	}
	// Attachments are the only fork data outside SQLite. If any later step
	// fails, the transaction rolls back the rows but cannot roll back the
	// copied files, so remove the fork's fresh directory as well.
	committed := false
	defer func() {
		if !committed {
			_ = s.RemoveAttachmentDir(fork.ID)
		}
	}()
	if _, err := tx.Exec(
		`INSERT INTO sessions(id, workspace_id, title, created_at, updated_at, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fork.ID, fork.WorkspaceID, fork.Title,
		encodeTime(fork.CreatedAt), encodeTime(fork.UpdatedAt), string(fork.Status),
	); err != nil {
		return nil, fmt.Errorf("store: fork session %q: insert session: %w", sessionID, err)
	}

	messageIDs, err := copyForkMessages(tx, sessionID, fork.ID)
	if err != nil {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, err)
	}
	if err := copyForkEvents(tx, sessionID, fork.ID); err != nil {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, err)
	}
	if err := copyForkToolCalls(tx, sessionID, fork.ID, messageIDs); err != nil {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, err)
	}
	if err := copyForkFeedback(tx, sessionID, fork.ID, messageIDs); err != nil {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, err)
	}
	if err := s.copyForkAttachments(tx, sessionID, fork.ID, messageIDs); err != nil {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, err)
	}
	if err := copyForkSettings(tx, sessionID, fork.ID); err != nil {
		return nil, fmt.Errorf("store: fork session %q: %w", sessionID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: fork session %q: commit: %w", sessionID, err)
	}
	committed = true
	return fork, nil
}

// forkMessage is one source message while the transaction is being built.
type forkMessage struct {
	id      string
	role    string
	content string
	reason  string
	created string
	turnID  string
}

// copyForkMessages copies every message of sourceID to newSessionID, keeping
// the append order, timestamps and turn grouping (which are meaningful) but
// assigning fresh message ids and dense per-session ordinals (AR-07). It
// returns the old-id -> new-id mapping used to rewire tool calls and feedback.
func copyForkMessages(tx *sql.Tx, sourceID, newSessionID string) (map[string]string, error) {
	rows, err := tx.Query(
		`SELECT `+messageColumns+` FROM messages WHERE session_id = ?
		 ORDER BY ordinal, id`, sourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("copy messages: query: %w", err)
	}
	var list []forkMessage
	for rows.Next() {
		var (
			m              forkMessage
			ignoredID      string
			ignoredOrdinal int64
		)
		if err := rows.Scan(&m.id, &ignoredID, &m.role, &m.content, &m.reason, &m.created, &m.turnID, &ignoredOrdinal); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return nil, fmt.Errorf("copy messages: scan: %w", err)
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return nil, fmt.Errorf("copy messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("copy messages: close: %w", err)
	}

	ids := make(map[string]string, len(list))
	for i, m := range list {
		newMessageID, err := newID()
		if err != nil {
			return nil, err
		}
		ids[m.id] = newMessageID
		if _, err := tx.Exec(
			`INSERT INTO messages(id, session_id, role, content, reasoning, created_at, turn_id, ordinal)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			newMessageID, newSessionID, m.role, m.content, m.reason, m.created, m.turnID, int64(i+1),
		); err != nil {
			return nil, fmt.Errorf("copy messages: insert: %w", err)
		}
	}
	return ids, nil
}

// forkEvent is one source event while the transaction is being built.
type forkEvent struct {
	eventType string
	payload   sql.NullString
	ts        string
}

// copyForkEvents copies the event trace, renumbering seq from 1 so the fork's
// sequence is dense and independent of the source. Event payloads are opaque
// and copied verbatim.
func copyForkEvents(tx *sql.Tx, sourceID, newSessionID string) error {
	rows, err := tx.Query(
		`SELECT type, payload, ts FROM events WHERE session_id = ? ORDER BY seq`, sourceID,
	)
	if err != nil {
		return fmt.Errorf("copy events: query: %w", err)
	}
	var list []forkEvent
	for rows.Next() {
		var ev forkEvent
		if err := rows.Scan(&ev.eventType, &ev.payload, &ev.ts); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return fmt.Errorf("copy events: scan: %w", err)
		}
		list = append(list, ev)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return fmt.Errorf("copy events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("copy events: close: %w", err)
	}

	for i, ev := range list {
		var payload any
		if ev.payload.Valid {
			payload = ev.payload.String
		}
		if _, err := tx.Exec(
			`INSERT INTO events(session_id, seq, type, payload, ts) VALUES (?, ?, ?, ?, ?)`,
			newSessionID, i+1, ev.eventType, payload, ev.ts,
		); err != nil {
			return fmt.Errorf("copy events: insert: %w", err)
		}
	}
	return nil
}

// forkToolCall is one source tool call while the transaction is being built.
type forkToolCall struct {
	messageID sql.NullString
	name      string
	args      string
	result    string
	status    string
	started   string
	ended     string
	approval  string
}

// copyForkToolCalls copies tool_calls rows with fresh call ids and rewires the
// message association to the copied message.
func copyForkToolCalls(tx *sql.Tx, sourceID, newSessionID string, messageIDs map[string]string) error {
	rows, err := tx.Query(
		`SELECT message_id, name, args, result, status, started_at, ended_at, approval
		   FROM tool_calls WHERE session_id = ? ORDER BY started_at, id`, sourceID,
	)
	if err != nil {
		return fmt.Errorf("copy tool calls: query: %w", err)
	}
	var list []forkToolCall
	for rows.Next() {
		var call forkToolCall
		if err := rows.Scan(&call.messageID, &call.name, &call.args, &call.result,
			&call.status, &call.started, &call.ended, &call.approval); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return fmt.Errorf("copy tool calls: scan: %w", err)
		}
		list = append(list, call)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return fmt.Errorf("copy tool calls: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("copy tool calls: close: %w", err)
	}

	for _, call := range list {
		callID, err := newID()
		if err != nil {
			return err
		}
		var messageID any
		if call.messageID.Valid {
			if mapped, ok := messageIDs[call.messageID.String]; ok {
				messageID = mapped
			} else {
				messageID = call.messageID.String
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO tool_calls(id, session_id, message_id, name, args, result, status, started_at, ended_at, approval)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			callID, newSessionID, messageID, call.name, call.args, call.result,
			call.status, call.started, call.ended, call.approval,
		); err != nil {
			return fmt.Errorf("copy tool calls: insert: %w", err)
		}
	}
	return nil
}

// forkFeedback is one source feedback row while the transaction is being built.
type forkFeedback struct {
	messageID string
	rating    string
	note      string
	created   string
}

// copyForkFeedback carries feedback annotations onto the matching copied
// messages, so a fork reflects the same ratings.
func copyForkFeedback(tx *sql.Tx, sourceID, newSessionID string, messageIDs map[string]string) error {
	rows, err := tx.Query(
		`SELECT message_id, rating, note, created_at FROM feedback
		  WHERE session_id = ? ORDER BY created_at, rowid`, sourceID,
	)
	if err != nil {
		return fmt.Errorf("copy feedback: query: %w", err)
	}
	var list []forkFeedback
	for rows.Next() {
		var f forkFeedback
		if err := rows.Scan(&f.messageID, &f.rating, &f.note, &f.created); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return fmt.Errorf("copy feedback: scan: %w", err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return fmt.Errorf("copy feedback: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("copy feedback: close: %w", err)
	}

	for _, f := range list {
		id, err := newID()
		if err != nil {
			return err
		}
		messageID := f.messageID
		if mapped, ok := messageIDs[f.messageID]; ok {
			messageID = mapped
		}
		if _, err := tx.Exec(
			`INSERT INTO feedback(`+feedbackColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
			id, newSessionID, messageID, f.rating, f.note, f.created,
		); err != nil {
			return fmt.Errorf("copy feedback: insert: %w", err)
		}
	}
	return nil
}

// forkSetting is one source settings row while the transaction is being built.
type forkSetting struct {
	key   string
	value string
}

// copyForkSettings copies keys scoped to the source session (the
// "session.<id>." namespace the controller uses for permission, mode, model
// and title markers) onto the fork.
func copyForkSettings(tx *sql.Tx, sourceID, newSessionID string) error {
	prefix := "session." + sourceID + "."
	rows, err := tx.Query(
		`SELECT key, value FROM settings WHERE instr(key, ?) = 1`, prefix,
	)
	if err != nil {
		return fmt.Errorf("copy settings: query: %w", err)
	}
	var list []forkSetting
	for rows.Next() {
		var st forkSetting
		if err := rows.Scan(&st.key, &st.value); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return fmt.Errorf("copy settings: scan: %w", err)
		}
		list = append(list, st)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return fmt.Errorf("copy settings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("copy settings: close: %w", err)
	}

	newPrefix := "session." + newSessionID + "."
	for _, st := range list {
		if _, err := tx.Exec(
			`INSERT INTO settings(key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			newPrefix+strings.TrimPrefix(st.key, prefix), st.value,
		); err != nil {
			return fmt.Errorf("copy settings: insert: %w", err)
		}
	}
	return nil
}

// copyForkAttachments duplicates the source session's attachment rows onto the
// fork: fresh ids, a message_id remapped through messageIDs, and a private
// copy of the bytes under the fork's attachment directory. Pending
// attachments (no message yet) stay pending; bytes that are missing or stored
// outside the attachment root are skipped, because a fork must not fail only
// because a file disappeared meanwhile.
//
// The caller removes the fork's attachment directory when the transaction does
// not commit, so a partial copy never survives as orphaned bytes.
func (s *Store) copyForkAttachments(tx *sql.Tx, sourceID, newSessionID string, messageIDs map[string]string) error {
	rows, err := tx.Query(
		`SELECT `+attachmentColumns+` FROM attachments WHERE session_id = ?
		 ORDER BY created_at, rowid`, sourceID,
	)
	if err != nil {
		return fmt.Errorf("copy attachments: query: %w", err)
	}
	var list []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return fmt.Errorf("copy attachments: scan: %w", err)
		}
		list = append(list, *a)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return fmt.Errorf("copy attachments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("copy attachments: close: %w", err)
	}
	if len(list) == 0 {
		return nil
	}

	dir, err := s.AttachmentDir(newSessionID)
	if err != nil {
		return fmt.Errorf("copy attachments: %w", err)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("copy attachments: create directory %q: %w", dir, err)
	}
	for _, a := range list {
		src, err := s.attachmentPath(a.Path)
		if err != nil {
			continue // persisted path outside the root: never read it
		}
		id, err := newID()
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, forkAttachmentFileName(id, a.Name))
		size, err := copyForkAttachmentFile(src, dest)
		if errors.Is(err, fs.ErrNotExist) {
			continue // the source file is gone; skip rather than fail the fork
		}
		if err != nil {
			return fmt.Errorf("copy attachments: %q: %w", a.Name, err)
		}
		var messageID any
		if strings.TrimSpace(a.MessageID) != "" {
			// A source row can only reference a copied message; unmapped rows
			// become pending instead of pointing at the source session.
			if mapped, ok := messageIDs[a.MessageID]; ok {
				messageID = mapped
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO attachments(`+attachmentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, newSessionID, messageID, a.Name, dest, a.MIME, a.Kind, size,
			encodeTime(a.CreatedAt),
		); err != nil {
			return fmt.Errorf("copy attachments: insert: %w", err)
		}
	}
	return nil
}

// forkAttachmentFileName names a copied attachment in the fork's directory:
// the fresh id plus the source display name reduced to one path component.
func forkAttachmentFileName(id, name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.ReplaceAll(base, string(filepath.Separator), "_")
	if base == "" || base == "." || base == ".." {
		base = "attachment"
	}
	return id + "-" + base
}

// copyForkAttachmentFile copies src to dest, creating dest exclusively with
// owner-only permissions, and returns the copied byte count.
func copyForkAttachmentFile(src, dest string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close() //nolint:errcheck // copy error wins

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return 0, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		return 0, err
	}
	return n, nil
}
