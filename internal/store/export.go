package store

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// exportSessionLine is the JSONL header record: the session row plus a marker
// distinguishing it from message, attachment, event and feedback records.
type exportSessionLine struct {
	Type string `json:"type"`
	Session
}

// exportMessageLine is one message row in the JSONL export.
type exportMessageLine struct {
	Type string `json:"type"`
	Message
}

// exportEventLine is one event row in the JSONL export. Event.Type is renamed
// event_type so the record's own type stays "event" for every line's schema.
type exportEventLine struct {
	Type      string          `json:"type"`
	EventType string          `json:"event_type"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	TS        time.Time       `json:"ts"`
}

// exportFeedbackLine is one feedback row in the JSONL export.
type exportFeedbackLine struct {
	Type string `json:"type"`
	Feedback
}

// exportAttachmentLine is one attachment row in the JSONL export. A session
// log is meant to be self-contained: Data carries the stored bytes as base64
// (for every kind, including images and other binaries), and Text repeats the
// body verbatim for text attachments whose bytes are valid UTF-8 so the log
// also stays greppable. An attachment whose file disappeared is exported as
// metadata only rather than failing the whole export.
type exportAttachmentLine struct {
	Type string `json:"type"`
	Attachment
	Encoding string `json:"encoding,omitempty"`
	Data     string `json:"data,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ExportSessionJSONL renders a session and its durable history as JSONL: one
// header record, then every message, attachment, event and feedback row in
// append order. Each line carries a "type" of session, message, attachment,
// event or feedback.
//
// All reads happen in one transaction over the store's single connection, so
// a concurrent turn cannot produce a torn export. An unknown session returns
// ErrNotFound.
func (s *Store) ExportSessionJSONL(sessionID string) ([]byte, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: export session: empty id", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: export session %q: begin: %w", sessionID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	sess, err := scanSession(tx.QueryRow(
		`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, sessionID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: export session %q: %w", sessionID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: export session %q: read session: %w", sessionID, err)
	}

	messages, err := exportMessages(tx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: export session %q: %w", sessionID, err)
	}
	events, err := exportEvents(tx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: export session %q: %w", sessionID, err)
	}
	feedback, err := exportFeedback(tx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: export session %q: %w", sessionID, err)
	}
	attachments, err := s.exportAttachments(tx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: export session %q: %w", sessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: export session %q: commit: %w", sessionID, err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(exportSessionLine{Type: "session", Session: *sess}); err != nil {
		return nil, fmt.Errorf("store: export session %q: encode session: %w", sessionID, err)
	}
	for _, m := range messages {
		if err := enc.Encode(exportMessageLine{Type: "message", Message: m}); err != nil {
			return nil, fmt.Errorf("store: export session %q: encode message: %w", sessionID, err)
		}
	}
	for _, a := range attachments {
		if err := enc.Encode(a); err != nil {
			return nil, fmt.Errorf("store: export session %q: encode attachment: %w", sessionID, err)
		}
	}
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return nil, fmt.Errorf("store: export session %q: encode event: %w", sessionID, err)
		}
	}
	for _, f := range feedback {
		if err := enc.Encode(exportFeedbackLine{Type: "feedback", Feedback: f}); err != nil {
			return nil, fmt.Errorf("store: export session %q: encode feedback: %w", sessionID, err)
		}
	}
	return buf.Bytes(), nil
}

// exportMessages reads every message of sessionID, ordered like ListMessages.
func exportMessages(tx *sql.Tx, sessionID string) ([]Message, error) {
	rows, err := tx.Query(
		`SELECT `+messageColumns+` FROM messages WHERE session_id = ?
		 ORDER BY created_at, rowid`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	var out []Message
	for rows.Next() {
		var (
			m       Message
			created string
		)
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Reasoning, &created, &m.TurnID); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return nil, fmt.Errorf("read messages: scan: %w", err)
		}
		if m.CreatedAt, err = decodeTime(created); err != nil {
			rows.Close() //nolint:errcheck // decode error wins
			return nil, fmt.Errorf("read messages: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return nil, fmt.Errorf("read messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read messages: close: %w", err)
	}
	return out, nil
}

// exportEvents reads every event of sessionID in sequence order.
func exportEvents(tx *sql.Tx, sessionID string) ([]exportEventLine, error) {
	rows, err := tx.Query(
		`SELECT seq, type, payload, ts FROM events WHERE session_id = ? ORDER BY seq`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	var out []exportEventLine
	for rows.Next() {
		var (
			ev      exportEventLine
			payload sql.NullString
			ts      string
		)
		if err := rows.Scan(&ev.Seq, &ev.EventType, &payload, &ts); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return nil, fmt.Errorf("read events: scan: %w", err)
		}
		ev.Type = "event"
		ev.SessionID = sessionID
		if ev.TS, err = decodeTime(ts); err != nil {
			rows.Close() //nolint:errcheck // decode error wins
			return nil, fmt.Errorf("read events: %w", err)
		}
		if payload.Valid && payload.String != "" {
			ev.Payload = append(json.RawMessage(nil), payload.String...)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return nil, fmt.Errorf("read events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read events: close: %w", err)
	}
	return out, nil
}

// exportAttachments reads every attachment row of sessionID in append order
// and embeds the stored bytes. A file that cannot be read stays a metadata
// record: losing one blob must not make the rest of the log unexportable.
func (s *Store) exportAttachments(tx *sql.Tx, sessionID string) ([]exportAttachmentLine, error) {
	rows, err := tx.Query(
		`SELECT `+attachmentColumns+` FROM attachments WHERE session_id = ?
		 ORDER BY created_at, rowid`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("read attachments: %w", err)
	}
	var out []exportAttachmentLine
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return nil, fmt.Errorf("read attachments: scan: %w", err)
		}
		line := exportAttachmentLine{Type: "attachment", Attachment: *a}
		if data, err := s.ReadAttachmentData(*a, 0); err == nil {
			line.Encoding = "base64"
			line.Data = base64.StdEncoding.EncodeToString(data)
			if a.Kind == AttachmentKindText && utf8.Valid(data) {
				line.Text = string(data)
			}
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return nil, fmt.Errorf("read attachments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read attachments: close: %w", err)
	}
	return out, nil
}

// exportFeedback reads every feedback row of sessionID in append order.
func exportFeedback(tx *sql.Tx, sessionID string) ([]Feedback, error) {
	rows, err := tx.Query(
		`SELECT `+feedbackColumns+` FROM feedback WHERE session_id = ?
		 ORDER BY created_at, rowid`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("read feedback: %w", err)
	}
	var out []Feedback
	for rows.Next() {
		var (
			f       Feedback
			created string
		)
		if err := rows.Scan(&f.ID, &f.SessionID, &f.MessageID, &f.Rating, &f.Note, &created); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return nil, fmt.Errorf("read feedback: scan: %w", err)
		}
		if f.CreatedAt, err = decodeTime(created); err != nil {
			rows.Close() //nolint:errcheck // decode error wins
			return nil, fmt.Errorf("read feedback: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // iteration error wins
		return nil, fmt.Errorf("read feedback: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read feedback: close: %w", err)
	}
	return out, nil
}
