package store

import (
	"fmt"
	"strings"
	"time"
)

// ReplaceCompactedMessages is the durable half of a manual /compact: it
// replaces a prefix of a session's earlier messages with one summary message in
// a single transaction, so a reopened session, the model history derived from
// the messages table and the live transcript all agree on the compacted
// conversation surface.
//
// replacedIDs must be the session's current messages in append order starting
// at the first message (compaction always replaces earlier history and keeps a
// recent tail). The kept tail rows are not touched — their ids, turn grouping
// and timestamps survive — and their ordinals are renumbered dense behind the
// summary, so later appends continue the per-session ordinal sequence (AR-07).
// The summary message takes the replaced prefix's place on the timeline (its
// CreatedAt is placed just before the earliest kept message) and its content is
// persisted like any other row.
//
// Tool calls are not part of the message transcript but the model history
// interleaves them: keepToolCallIDs names the calls whose results are still
// reachable from the compacted tail, and every other recorded call of the
// session (all from the replaced prefix) is deleted in the same transaction.
// Attachments/feedback rows that reference replaced messages are detached by
// the schema's ON DELETE SET NULL foreign keys.
//
// The method is transactional: a validation failure rolls back without
// changing the session.
func (s *Store) ReplaceCompactedMessages(
	sessionID string,
	replacedIDs []string,
	summary Message,
	keepToolCallIDs []string,
) (*Message, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: replace compacted messages: empty session id", ErrInvalidArgument)
	}
	if len(replacedIDs) == 0 {
		return nil, fmt.Errorf("%w: replace compacted messages: no replaced message ids", ErrInvalidArgument)
	}
	if strings.TrimSpace(summary.Role) == "" {
		return nil, fmt.Errorf("%w: replace compacted messages: empty summary role", ErrInvalidArgument)
	}
	if strings.TrimSpace(summary.Content) == "" {
		return nil, fmt.Errorf("%w: replace compacted messages: empty summary content", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: replace compacted messages: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	ok, err := sessionExists(tx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: replace compacted messages: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("store: replace compacted messages: session %q: %w", sessionID, ErrNotFound)
	}

	// Read the persisted conversation; the replaced ids must match its current
	// prefix exactly, in append order.
	rows, err := tx.Query(
		`SELECT `+messageColumns+` FROM messages WHERE session_id = ? ORDER BY ordinal, id`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: replace compacted messages for session %q: query: %w", sessionID, err)
	}
	var messages []Message
	for rows.Next() {
		var (
			m       Message
			created string
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Reasoning, &created, &m.TurnID, &m.Ordinal); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return nil, fmt.Errorf("store: replace compacted messages for session %q: scan: %w", sessionID, err)
		}
		if m.CreatedAt, err = decodeTime(created); err != nil {
			rows.Close() //nolint:errcheck // decode error wins
			return nil, fmt.Errorf("store: replace compacted messages for session %q: %w", sessionID, err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return nil, fmt.Errorf("store: replace compacted messages for session %q: %w", sessionID, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: replace compacted messages for session %q: close: %w", sessionID, err)
	}

	if len(replacedIDs) > len(messages) {
		return nil, fmt.Errorf(
			"%w: replace compacted messages for session %q: %d replaced ids, session has %d messages",
			ErrInvalidArgument, sessionID, len(replacedIDs), len(messages))
	}
	for i, id := range replacedIDs {
		if messages[i].ID != id {
			return nil, fmt.Errorf(
				"%w: replace compacted messages for session %q: id %d is %q, not the requested %q (the replaced scope must be the current message prefix in order)",
				ErrInvalidArgument, sessionID, i, messages[i].ID, id)
		}
	}
	kept := messages[len(replacedIDs):]

	// Remove the replaced rows. tool_calls.message_id / attachments.message_id
	// are nullable with ON DELETE SET NULL, so dependent rows are detached
	// rather than cascading away; the tool calls of the replaced turns are
	// deleted explicitly below.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(replacedIDs)), ",")
	deleteArgs := make([]any, 0, len(replacedIDs)+1)
	deleteArgs = append(deleteArgs, sessionID)
	for _, id := range replacedIDs {
		deleteArgs = append(deleteArgs, id)
	}
	if _, err := tx.Exec(
		`DELETE FROM messages WHERE session_id = ? AND id IN (`+placeholders+`)`, deleteArgs...,
	); err != nil {
		return nil, fmt.Errorf("store: replace compacted messages for session %q: delete: %w", sessionID, err)
	}

	// Every tool call that is not part of the compacted tail belonged to the
	// replaced turns.
	keep := make([]string, 0, len(keepToolCallIDs))
	seen := make(map[string]bool, len(keepToolCallIDs))
	for _, id := range keepToolCallIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		keep = append(keep, id)
	}
	if len(keep) == 0 {
		if _, err := tx.Exec(`DELETE FROM tool_calls WHERE session_id = ?`, sessionID); err != nil {
			return nil, fmt.Errorf("store: replace compacted messages for session %q: delete tool calls: %w", sessionID, err)
		}
	} else {
		args := make([]any, 0, len(keep)+1)
		args = append(args, sessionID)
		for _, id := range keep {
			args = append(args, id)
		}
		if _, err := tx.Exec(
			`DELETE FROM tool_calls WHERE session_id = ? AND id NOT IN (`+
				strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",")+`)`, args...,
		); err != nil {
			return nil, fmt.Errorf("store: replace compacted messages for session %q: delete tool calls: %w", sessionID, err)
		}
	}

	// Renumber the kept tail dense behind the summary and put the summary at
	// the conversation's start. The shift is only needed when more than one
	// message was replaced (the first kept row is then the second message).
	if len(kept) > 0 && len(replacedIDs) > 1 {
		if _, err := tx.Exec(
			`UPDATE messages SET ordinal = ordinal - ? WHERE session_id = ?`,
			int64(len(replacedIDs)-1), sessionID,
		); err != nil {
			return nil, fmt.Errorf("store: replace compacted messages for session %q: renumber: %w", sessionID, err)
		}
	}

	out := summary
	out.SessionID = sessionID
	if out.ID == "" {
		id, idErr := newID()
		if idErr != nil {
			return nil, idErr
		}
		out.ID = id
	}
	if out.TurnID == "" && len(messages) > 0 {
		// The summary stands in for the replaced turns; naming the first
		// replaced turn keeps export/fork grouping sensible.
		out.TurnID = messages[0].TurnID
	}
	// Place the summary just before the earliest kept message so the
	// storeHistory interleaving (which orders tool calls against message
	// timestamps) emits surviving tool calls after the summary, never above it.
	base := nowUTC()
	if len(messages) > 0 && !messages[0].CreatedAt.IsZero() {
		base = messages[0].CreatedAt
	}
	out.CreatedAt = base.Add(-time.Millisecond).UTC()
	out.Ordinal = 1
	if _, err := tx.Exec(
		`INSERT INTO messages(`+messageColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		out.ID, out.SessionID, out.Role, out.Content, out.Reasoning,
		encodeTime(out.CreatedAt), out.TurnID, out.Ordinal,
	); err != nil {
		return nil, fmt.Errorf("store: replace compacted messages for session %q: insert summary: %w", sessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: replace compacted messages for session %q: commit: %w", sessionID, err)
	}
	return &out, nil
}
