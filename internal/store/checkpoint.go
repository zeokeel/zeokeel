package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Checkpoint is the durable recovery state of one session at a turn
// boundary. History holds the conversation as JSON (the runtime's Message
// slice); the store deliberately does not know the runtime's message shape, so
// the app adapter owns encoding and decoding it.
type Checkpoint struct {
	SessionID string
	History   json.RawMessage
	TurnID    string
	UpdatedAt time.Time
	Phase     string
}

const checkpointColumns = `session_id, history, turn_id, updated_at, phase`

// SaveCheckpoint inserts or replaces the checkpoint for cp.SessionID. The
// session must exist; a missing session reports ErrNotFound instead of a raw
// foreign-key error. A zero UpdatedAt is replaced with the current UTC time.
func (s *Store) SaveCheckpoint(cp Checkpoint) error {
	if cp.SessionID == "" {
		return fmt.Errorf("%w: save checkpoint: empty session id", ErrInvalidArgument)
	}
	updated := cp.UpdatedAt
	if updated.IsZero() {
		updated = nowUTC()
	} else {
		updated = updated.UTC()
	}
	history := cp.History
	if len(history) == 0 {
		history = json.RawMessage("[]")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: save checkpoint: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	ok, err := sessionExists(tx, cp.SessionID)
	if err != nil {
		return fmt.Errorf("store: save checkpoint: %w", err)
	}
	if !ok {
		return fmt.Errorf("store: save checkpoint: session %q: %w", cp.SessionID, ErrNotFound)
	}
	if _, err := tx.Exec(
		`INSERT INTO checkpoints(`+checkpointColumns+`) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   history = excluded.history,
		   turn_id = excluded.turn_id,
		   updated_at = excluded.updated_at,
		   phase = excluded.phase`,
		cp.SessionID, string(history), cp.TurnID, encodeTime(updated), cp.Phase,
	); err != nil {
		return fmt.Errorf("store: save checkpoint for session %q: insert: %w", cp.SessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: save checkpoint for session %q: commit: %w", cp.SessionID, err)
	}
	return nil
}

// LoadCheckpoint returns the checkpoint for sessionID, or an error wrapping
// ErrNotFound when the session has none.
func (s *Store) LoadCheckpoint(sessionID string) (*Checkpoint, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: load checkpoint: empty session id", ErrInvalidArgument)
	}

	var (
		cp      Checkpoint
		history string
		updated string
	)
	err := s.db.QueryRow(
		`SELECT `+checkpointColumns+` FROM checkpoints WHERE session_id = ?`,
		sessionID,
	).Scan(&cp.SessionID, &history, &cp.TurnID, &updated, &cp.Phase)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: load checkpoint %q: %w", sessionID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: load checkpoint %q: %w", sessionID, err)
	}
	if cp.UpdatedAt, err = decodeTime(updated); err != nil {
		return nil, fmt.Errorf("store: load checkpoint %q: %w", sessionID, err)
	}
	cp.History = append(json.RawMessage(nil), history...)
	return &cp, nil
}

// DeleteCheckpoint removes the checkpoint for sessionID. A missing checkpoint
// is not an error: deletion is a cleanup operation, so it is idempotent.
func (s *Store) DeleteCheckpoint(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("%w: delete checkpoint: empty session id", ErrInvalidArgument)
	}
	if _, err := s.db.Exec(`DELETE FROM checkpoints WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("store: delete checkpoint %q: %w", sessionID, err)
	}
	return nil
}
