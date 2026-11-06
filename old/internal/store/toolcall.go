package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ToolCall is one row of the tool_calls table: the durable record of a tool
// invocation the model made during a turn. It is independent of the events
// table (the event trace keeps the runtime's order); this table is the
// queryable per-call view.
type ToolCall struct {
	ID        string
	SessionID string
	MessageID string
	Name      string
	Args      string
	Result    string
	Status    string // running, done, error
	StartedAt time.Time
	EndedAt   time.Time
	Approval  string
}

// RecordToolCall inserts or updates one tool call. A second call with the same
// id updates the result/status/end time of the first, which is how the
// session controller closes a call it recorded at tool_start.
func (s *Store) RecordToolCall(call ToolCall) error {
	if call.ID == "" {
		return fmt.Errorf("%w: record tool call: empty id", ErrInvalidArgument)
	}
	if call.SessionID == "" {
		return fmt.Errorf("%w: record tool call: empty session id", ErrInvalidArgument)
	}
	if call.Name == "" {
		return fmt.Errorf("%w: record tool call: empty name", ErrInvalidArgument)
	}
	if call.Status == "" {
		call.Status = "running"
	}
	started := call.StartedAt
	if started.IsZero() {
		started = nowUTC()
	}
	ended := call.EndedAt
	if ended.IsZero() {
		ended = started
	}
	_, err := s.db.Exec(
		`INSERT INTO tool_calls(id, session_id, message_id, name, args, result, status, started_at, ended_at, approval)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   result = excluded.result,
		   status = excluded.status,
		   ended_at = excluded.ended_at`,
		call.ID, call.SessionID, nullString(call.MessageID), call.Name,
		call.Args, call.Result, call.Status,
		encodeTime(started), encodeTime(ended), call.Approval,
	)
	if err != nil {
		return fmt.Errorf("store: record tool call %q: %w", call.ID, err)
	}
	return nil
}

// FinishToolCall closes a call recorded at tool_start. A missing row is not an
// error: the durable record is a tracing aid, not a turn dependency.
func (s *Store) FinishToolCall(id, result, status string, ended time.Time) error {
	if id == "" {
		return fmt.Errorf("%w: finish tool call: empty id", ErrInvalidArgument)
	}
	if status == "" {
		status = "done"
	}
	if ended.IsZero() {
		ended = nowUTC()
	}
	if _, err := s.db.Exec(
		`UPDATE tool_calls SET result = ?, status = ?, ended_at = ? WHERE id = ?`,
		result, status, encodeTime(ended), id,
	); err != nil {
		return fmt.Errorf("store: finish tool call %q: %w", id, err)
	}
	return nil
}

// GetToolCall returns one recorded tool call.
func (s *Store) GetToolCall(id string) (*ToolCall, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: get tool call: empty id", ErrInvalidArgument)
	}
	var (
		call      ToolCall
		messageID sql.NullString
		started   string
		ended     string
	)
	err := s.db.QueryRow(
		`SELECT id, session_id, message_id, name, args, result, status, started_at, ended_at, approval
		 FROM tool_calls WHERE id = ?`, id,
	).Scan(&call.ID, &call.SessionID, &messageID, &call.Name, &call.Args,
		&call.Result, &call.Status, &started, &ended, &call.Approval)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get tool call %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get tool call %q: %w", id, err)
	}
	call.MessageID = messageID.String
	if call.StartedAt, err = decodeTime(started); err != nil {
		return nil, fmt.Errorf("store: get tool call %q: started_at: %w", id, err)
	}
	if call.EndedAt, err = decodeTime(ended); err != nil {
		return nil, fmt.Errorf("store: get tool call %q: ended_at: %w", id, err)
	}
	return &call, nil
}

// ToolCallsForSession lists a session's recorded tool calls in start order.
func (s *Store) ToolCallsForSession(sessionID string) ([]ToolCall, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: list tool calls: empty session id", ErrInvalidArgument)
	}
	rows, err := s.db.Query(
		`SELECT id, session_id, message_id, name, args, result, status, started_at, ended_at, approval
		 FROM tool_calls WHERE session_id = ? ORDER BY started_at, id`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list tool calls for session %q: %w", sessionID, err)
	}
	defer rows.Close()
	var out []ToolCall
	for rows.Next() {
		var (
			call      ToolCall
			messageID sql.NullString
			started   string
			ended     string
		)
		if err := rows.Scan(&call.ID, &call.SessionID, &messageID, &call.Name, &call.Args,
			&call.Result, &call.Status, &started, &ended, &call.Approval); err != nil {
			return nil, fmt.Errorf("store: list tool calls for session %q: scan: %w", sessionID, err)
		}
		call.MessageID = messageID.String
		if call.StartedAt, err = decodeTime(started); err != nil {
			return nil, err
		}
		if call.EndedAt, err = decodeTime(ended); err != nil {
			return nil, err
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list tool calls for session %q: %w", sessionID, err)
	}
	return out, nil
}

// nullString maps "" to SQL NULL for nullable text columns.
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
