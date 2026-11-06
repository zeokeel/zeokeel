package store

import (
	"fmt"
	"strings"
	"time"
)

// Feedback is one append-only thumbs/feedback record. Rating is "good" or
// "bad" for message-level ratings; session-level markers may use other short
// labels. MessageID is empty for a rating not tied to a message (the session
// menu uses the synthetic id "session" for its explicit marker).
//
// Rows are never updated: the newest row for a message wins when the UI
// reflects the current rating, while older rows stay available as history.
type Feedback struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	MessageID string    `json:"message_id,omitempty"`
	Rating    string    `json:"rating"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

const feedbackColumns = `id, session_id, message_id, rating, note, created_at`

// AppendFeedback persists one feedback row and returns the stored record. An
// empty ID and zero CreatedAt are filled in by the store.
func (s *Store) AppendFeedback(f Feedback) (*Feedback, error) {
	if strings.TrimSpace(f.SessionID) == "" {
		return nil, fmt.Errorf("%w: append feedback: empty session id", ErrInvalidArgument)
	}
	rating := strings.TrimSpace(f.Rating)
	if rating == "" {
		return nil, fmt.Errorf("%w: append feedback: empty rating", ErrInvalidArgument)
	}
	out := f
	out.Rating = rating
	out.MessageID = strings.TrimSpace(out.MessageID)
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
		return nil, fmt.Errorf("store: append feedback: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	ok, err := sessionExists(tx, out.SessionID)
	if err != nil {
		return nil, fmt.Errorf("store: append feedback: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("store: append feedback: session %q: %w", out.SessionID, ErrNotFound)
	}
	if _, err := tx.Exec(
		`INSERT INTO feedback(`+feedbackColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		out.ID, out.SessionID, out.MessageID, out.Rating, out.Note, encodeTime(out.CreatedAt),
	); err != nil {
		return nil, fmt.Errorf("store: append feedback to session %q: insert: %w", out.SessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: append feedback to session %q: commit: %w", out.SessionID, err)
	}
	return &out, nil
}

// ListFeedback returns a session's feedback rows in append order.
func (s *Store) ListFeedback(sessionID string) ([]Feedback, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: list feedback: empty session id", ErrInvalidArgument)
	}
	rows, err := s.db.Query(
		`SELECT `+feedbackColumns+` FROM feedback WHERE session_id = ?
		 ORDER BY created_at, rowid`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list feedback for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	list := make([]Feedback, 0)
	for rows.Next() {
		var (
			f       Feedback
			created string
		)
		if err := rows.Scan(&f.ID, &f.SessionID, &f.MessageID, &f.Rating, &f.Note, &created); err != nil {
			return nil, fmt.Errorf("store: list feedback for session %q: scan: %w", sessionID, err)
		}
		if f.CreatedAt, err = decodeTime(created); err != nil {
			return nil, fmt.Errorf("store: list feedback for session %q: %w", sessionID, err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list feedback for session %q: %w", sessionID, err)
	}
	return list, nil
}

// FeedbackByMessage returns the newest rating for each message id. A
// message-level row supersedes older rows for the same message; rows with an
// empty message id are ignored because they cannot be attached to a message.
func (s *Store) FeedbackByMessage(sessionID string) (map[string]Feedback, error) {
	list, err := s.ListFeedback(sessionID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Feedback)
	for _, f := range list {
		if strings.TrimSpace(f.MessageID) == "" {
			continue
		}
		out[f.MessageID] = f
	}
	return out, nil
}
