package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxMemoryContentBytes bounds one memory's content. The cap is enforced on
// write and again when the prompt renders the memory, so a database edited
// outside this API still cannot flood the instruction.
const MaxMemoryContentBytes = 8 * 1024

const (
	// DefaultMemoryLimit is the page size used when a caller passes a
	// non-positive limit.
	DefaultMemoryLimit = 50
	// MaxMemoryLimit bounds one listing or search. It exists so a caller
	// cannot ask SQLite for an unbounded result set.
	MaxMemoryLimit = 200
)

// Memory is one persistent, workspace-scoped note. Memories survive sessions
// and are injected into future turns, so the id is stable across updates while
// CreatedAt stays fixed and UpdatedAt moves.
type Memory struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// memoryColumns is the column order every read and write uses.
const memoryColumns = `id, workspace_id, content, created_at, updated_at`

// normalizeMemoryWorkspace validates the scope every operation needs.
func normalizeMemoryWorkspace(workspaceID string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", fmt.Errorf("%w: memory: empty workspace id", ErrInvalidArgument)
	}
	return workspaceID, nil
}

// normalizeMemoryContent trims and validates one memory body.
func normalizeMemoryContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("%w: memory: empty content", ErrInvalidArgument)
	}
	if len(content) > MaxMemoryContentBytes {
		return "", fmt.Errorf("%w: memory: content is %d bytes, maximum is %d",
			ErrInvalidArgument, len(content), MaxMemoryContentBytes)
	}
	return content, nil
}

// memoryLimit clamps a requested page size into [1, MaxMemoryLimit].
func memoryLimit(limit int) int {
	if limit <= 0 {
		return DefaultMemoryLimit
	}
	if limit > MaxMemoryLimit {
		return MaxMemoryLimit
	}
	return limit
}

// CreateMemory persists one note scoped to workspaceID and returns the stored
// row with its generated id and timestamps.
func (s *Store) CreateMemory(workspaceID, content string) (*Memory, error) {
	workspaceID, err := normalizeMemoryWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	content, err = normalizeMemoryContent(content)
	if err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	m := &Memory{
		ID:          id,
		WorkspaceID: workspaceID,
		Content:     content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create memory: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(
		`INSERT INTO memories(`+memoryColumns+`) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.WorkspaceID, m.Content, encodeTime(m.CreatedAt), encodeTime(m.UpdatedAt),
	); err != nil {
		return nil, fmt.Errorf("store: create memory: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create memory: commit: %w", err)
	}
	return m, nil
}

// GetMemory returns the memory with id, or an error wrapping ErrNotFound when
// no such row exists. Callers that enforce a workspace scope compare the
// returned WorkspaceID.
func (s *Store) GetMemory(id string) (*Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: get memory: empty id", ErrInvalidArgument)
	}
	row := s.db.QueryRow(`SELECT `+memoryColumns+` FROM memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get memory %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get memory %q: %w", id, err)
	}
	return m, nil
}

// ListMemories returns the workspace's memories newest first. limit <= 0 uses
// DefaultMemoryLimit and values above MaxMemoryLimit are clamped.
func (s *Store) ListMemories(workspaceID string, limit int) ([]Memory, error) {
	workspaceID, err := normalizeMemoryWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT `+memoryColumns+` FROM memories WHERE workspace_id = ?
		 ORDER BY updated_at DESC, id DESC LIMIT ?`,
		workspaceID, memoryLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("store: list memories for workspace %q: %w", workspaceID, err)
	}
	defer rows.Close()
	return scanMemories(rows, workspaceID)
}

// SearchMemories returns the workspace's memories whose content contains
// query, newest first. Matching is case-insensitive for ASCII, literal for
// LIKE metacharacters, and bounded to DefaultMemoryLimit rows.
func (s *Store) SearchMemories(workspaceID, query string) ([]Memory, error) {
	workspaceID, err := normalizeMemoryWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: search memories: empty query", ErrInvalidArgument)
	}
	rows, err := s.db.Query(
		`SELECT `+memoryColumns+` FROM memories
		 WHERE workspace_id = ? AND content LIKE ? ESCAPE '\'
		 ORDER BY updated_at DESC, id DESC LIMIT ?`,
		workspaceID, "%"+escapeLike(query)+"%", DefaultMemoryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: search memories in workspace %q: %w", workspaceID, err)
	}
	defer rows.Close()
	return scanMemories(rows, workspaceID)
}

// UpdateMemory replaces one memory's content and moves UpdatedAt. The row's
// workspace and CreatedAt are preserved.
func (s *Store) UpdateMemory(id, content string) (*Memory, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: update memory: empty id", ErrInvalidArgument)
	}
	content, err := normalizeMemoryContent(content)
	if err != nil {
		return nil, err
	}
	now := nowUTC()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: update memory %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(
		`UPDATE memories SET content = ?, updated_at = ? WHERE id = ?`,
		content, encodeTime(now), id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: update memory %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: update memory %q: rows affected: %w", id, err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("store: update memory %q: %w", id, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: update memory %q: commit: %w", id, err)
	}
	return s.GetMemory(id)
}

// DeleteMemory removes one memory. A missing id reports ErrNotFound.
func (s *Store) DeleteMemory(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: delete memory: empty id", ErrInvalidArgument)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: delete memory %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete memory %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete memory %q: rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("store: delete memory %q: %w", id, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete memory %q: commit: %w", id, err)
	}
	return nil
}

// ClearMemories deletes every memory in one workspace and returns how many
// rows were removed. Other workspaces are untouched.
func (s *Store) ClearMemories(workspaceID string) (int, error) {
	workspaceID, err := normalizeMemoryWorkspace(workspaceID)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: clear memories for workspace %q: begin: %w", workspaceID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`DELETE FROM memories WHERE workspace_id = ?`, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("store: clear memories for workspace %q: %w", workspaceID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear memories for workspace %q: rows affected: %w", workspaceID, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: clear memories for workspace %q: commit: %w", workspaceID, err)
	}
	return int(affected), nil
}

// scanMemory reads one row in memoryColumns order.
func scanMemory(row scanner) (*Memory, error) {
	var (
		m                Memory
		created, updated string
	)
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.Content, &created, &updated); err != nil {
		return nil, err
	}
	var err error
	if m.CreatedAt, err = decodeTime(created); err != nil {
		return nil, err
	}
	if m.UpdatedAt, err = decodeTime(updated); err != nil {
		return nil, err
	}
	return &m, nil
}

// scanMemories reads every row and wraps scan failures with the workspace.
func scanMemories(rows *sql.Rows, workspaceID string) ([]Memory, error) {
	list := make([]Memory, 0)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan memories for workspace %q: %w", workspaceID, err)
		}
		list = append(list, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memories for workspace %q: %w", workspaceID, err)
	}
	return list, nil
}

// escapeLike neutralizes the LIKE metacharacters so a search for "100%" is
// literal. The caller pairs it with ESCAPE '\'.
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}
