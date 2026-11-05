package store

import (
	"fmt"
	"strings"
	"time"
)

// Message is one chat message in a session (docs/PLAN.md §9.1). Reasoning
// holds the model's chain of thought when the provider exposes one; TurnID
// groups the messages produced by one user turn.
type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Reasoning string    `json:"reasoning,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	TurnID    string    `json:"turn_id,omitempty"`
}

const messageColumns = `id, session_id, role, content, reasoning, created_at, turn_id`

// AppendMessage persists m and returns the stored message. When m.ID is empty
// a fresh UUID is assigned; when m.CreatedAt is zero the current time is used.
func (s *Store) AppendMessage(m Message) (*Message, error) {
	if m.SessionID == "" {
		return nil, fmt.Errorf("%w: append message: empty session id", ErrInvalidArgument)
	}
	if strings.TrimSpace(m.Role) == "" {
		return nil, fmt.Errorf("%w: append message: empty role", ErrInvalidArgument)
	}
	out := m
	if out.ID == "" {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		out.ID = id
	}
	if out.CreatedAt.IsZero() {
		out.CreatedAt = nowUTC()
	} else {
		out.CreatedAt = out.CreatedAt.UTC()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: append message: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	ok, err := sessionExists(tx, m.SessionID)
	if err != nil {
		return nil, fmt.Errorf("store: append message: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("store: append message: session %q: %w", m.SessionID, ErrNotFound)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages(`+messageColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		out.ID, out.SessionID, out.Role, out.Content, out.Reasoning,
		encodeTime(out.CreatedAt), out.TurnID,
	); err != nil {
		return nil, fmt.Errorf("store: append message to session %q: insert: %w", m.SessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: append message to session %q: commit: %w", m.SessionID, err)
	}
	return &out, nil
}

// ListMessages returns a session's messages in append order. A message's
// CreatedAt is unique in practice; rowid breaks ties deterministically.
func (s *Store) ListMessages(sessionID string) ([]Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: list messages: empty session id", ErrInvalidArgument)
	}
	rows, err := s.db.Query(
		`SELECT `+messageColumns+` FROM messages WHERE session_id = ? ORDER BY created_at, rowid`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list messages for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	list := make([]Message, 0)
	for rows.Next() {
		var (
			m       Message
			created string
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Reasoning, &created, &m.TurnID); err != nil {
			return nil, fmt.Errorf("store: list messages for session %q: scan: %w", sessionID, err)
		}
		if m.CreatedAt, err = decodeTime(created); err != nil {
			return nil, fmt.Errorf("store: list messages for session %q: %w", sessionID, err)
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list messages for session %q: %w", sessionID, err)
	}
	return list, nil
}
