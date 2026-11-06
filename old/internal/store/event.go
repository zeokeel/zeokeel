package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Event is one entry of a session's append-only runtime trace (docs/PLAN.md
// §9.1). It is stored both in the events table and, when the caller appends
// it with AppendTranscript, as one JSON line of session.jsonl.
//
// Payload is opaque JSON; the store validates that it is well-formed but does
// not interpret it.
type Event struct {
	SessionID string          `json:"session_id,omitempty"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	TS        time.Time       `json:"ts"`
}

// AppendEvent appends event to sessionID and returns a copy with the assigned
// per-session sequence number (starting at 1) and timestamp. The sequence is
// allocated inside the same transaction as the insert, so concurrent appends
// cannot produce gaps or duplicates.
//
// event.Seq is ignored; event.TS defaults to the current time when zero.
func (s *Store) AppendEvent(sessionID string, event Event) (*Event, error) {
	out, err := s.AppendEvents(sessionID, []Event{event})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// AppendEvents appends events to sessionID in one SQLite transaction and
// returns copies with their assigned contiguous per-session sequence numbers
// and timestamps. Callers that persist a burst of high-frequency events (for
// example streamed token chunks) can use it to pay one transaction instead of
// one per event while keeping the same ordered, gap-free trace.
//
// The events are validated before the transaction starts, so a malformed batch
// writes nothing. An empty batch is a no-op and returns nil.
func (s *Store) AppendEvents(sessionID string, events []Event) ([]Event, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: append event: empty session id", ErrInvalidArgument)
	}
	if len(events) == 0 {
		return nil, nil
	}

	// Validate and normalize the whole batch first: an invalid payload must
	// not leave a partially written batch behind.
	prepared := make([]Event, len(events))
	payloads := make([]any, len(events))
	for i, event := range events {
		if strings.TrimSpace(event.Type) == "" {
			return nil, fmt.Errorf("%w: append event: empty type", ErrInvalidArgument)
		}
		var payload any
		if p := bytes.TrimSpace(event.Payload); len(p) > 0 {
			if !json.Valid(p) {
				return nil, fmt.Errorf("%w: append event: payload is not valid JSON", ErrInvalidArgument)
			}
			payload = string(p)
		}
		ts := event.TS
		if ts.IsZero() {
			ts = nowUTC()
		} else {
			ts = ts.UTC()
		}
		prepared[i] = Event{Type: event.Type, TS: ts}
		if p := bytes.TrimSpace(event.Payload); len(p) > 0 {
			prepared[i].Payload = append(json.RawMessage(nil), p...)
		}
		payloads[i] = payload
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: append event: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	ok, err := sessionExists(tx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: append event: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("store: append event: session %q: %w", sessionID, ErrNotFound)
	}

	var base int64
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(seq), 0) FROM events WHERE session_id = ?`, sessionID,
	).Scan(&base); err != nil {
		return nil, fmt.Errorf("store: append event: allocate seq: %w", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO events(session_id, seq, type, payload, ts) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: append event: prepare insert: %w", err)
	}
	defer stmt.Close() //nolint:errcheck // closed by tx commit/rollback too

	out := make([]Event, len(events))
	for i := range events {
		seq := base + int64(i) + 1
		if _, err := stmt.Exec(sessionID, seq, prepared[i].Type, payloads[i], encodeTime(prepared[i].TS)); err != nil {
			return nil, fmt.Errorf("store: append event to session %q: insert: %w", sessionID, err)
		}
		out[i] = Event{
			SessionID: sessionID,
			Seq:       seq,
			Type:      prepared[i].Type,
			Payload:   prepared[i].Payload,
			TS:        prepared[i].TS,
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: append event to session %q: commit: %w", sessionID, err)
	}
	return out, nil
}

// ListEvents returns a session's events ordered by sequence number.
func (s *Store) ListEvents(sessionID string) ([]Event, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: list events: empty session id", ErrInvalidArgument)
	}
	rows, err := s.db.Query(
		`SELECT session_id, seq, type, payload, ts FROM events WHERE session_id = ? ORDER BY seq`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list events for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	list := make([]Event, 0)
	for rows.Next() {
		var (
			ev      Event
			payload sql.NullString
			ts      string
		)
		if err := rows.Scan(&ev.SessionID, &ev.Seq, &ev.Type, &payload, &ts); err != nil {
			return nil, fmt.Errorf("store: list events for session %q: scan: %w", sessionID, err)
		}
		if ev.TS, err = decodeTime(ts); err != nil {
			return nil, fmt.Errorf("store: list events for session %q: %w", sessionID, err)
		}
		if payload.Valid && payload.String != "" {
			ev.Payload = append(json.RawMessage(nil), payload.String...)
		}
		list = append(list, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list events for session %q: %w", sessionID, err)
	}
	return list, nil
}

// GetEvent returns one event of sessionID by its per-session sequence number.
// It returns an error wrapping ErrNotFound when the session has no such event.
// The method is additive: ListEvents remains the ordered full-trace accessor.
func (s *Store) GetEvent(sessionID string, seq int64) (*Event, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: get event: empty session id", ErrInvalidArgument)
	}
	if seq <= 0 {
		return nil, fmt.Errorf("%w: get event: seq must be positive", ErrInvalidArgument)
	}
	var (
		ev      Event
		payload sql.NullString
		ts      string
	)
	err := s.db.QueryRow(
		`SELECT session_id, seq, type, payload, ts
		   FROM events WHERE session_id = ? AND seq = ?`,
		sessionID, seq,
	).Scan(&ev.SessionID, &ev.Seq, &ev.Type, &payload, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get event %q#%d: %w", sessionID, seq, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get event %q#%d: %w", sessionID, seq, err)
	}
	if ev.TS, err = decodeTime(ts); err != nil {
		return nil, fmt.Errorf("store: get event %q#%d: %w", sessionID, seq, err)
	}
	if payload.Valid && payload.String != "" {
		ev.Payload = append(json.RawMessage(nil), payload.String...)
	}
	return &ev, nil
}
