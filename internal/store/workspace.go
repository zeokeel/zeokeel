package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Workspace is a project root registered in the index (docs/PLAN.md §9.1).
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Root      string    `json:"root"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateWorkspace registers a workspace and returns it with its generated id
// and creation time.
func (s *Store) CreateWorkspace(name, root string) (*Workspace, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: create workspace: empty name", ErrInvalidArgument)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	w := &Workspace{ID: id, Name: name, Root: root, CreatedAt: nowUTC()}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create workspace: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(
		`INSERT INTO workspaces(id, name, root, created_at) VALUES (?, ?, ?, ?)`,
		w.ID, w.Name, w.Root, encodeTime(w.CreatedAt),
	); err != nil {
		return nil, fmt.Errorf("store: create workspace %q: insert: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create workspace %q: commit: %w", name, err)
	}
	return w, nil
}

// ListWorkspaces returns all workspaces ordered by creation time.
func (s *Store) ListWorkspaces() ([]Workspace, error) {
	rows, err := s.db.Query(`SELECT id, name, root, created_at FROM workspaces ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list workspaces: %w", err)
	}
	defer rows.Close()

	list := make([]Workspace, 0)
	for rows.Next() {
		var (
			w       Workspace
			created string
		)
		if err := rows.Scan(&w.ID, &w.Name, &w.Root, &created); err != nil {
			return nil, fmt.Errorf("store: list workspaces: scan: %w", err)
		}
		if w.CreatedAt, err = decodeTime(created); err != nil {
			return nil, fmt.Errorf("store: list workspaces: %w", err)
		}
		list = append(list, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list workspaces: %w", err)
	}
	return list, nil
}

// GetWorkspace returns the workspace with id, or an error wrapping
// ErrNotFound when no such workspace exists.
func (s *Store) GetWorkspace(id string) (*Workspace, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: get workspace: empty id", ErrInvalidArgument)
	}
	var (
		w       Workspace
		created string
	)
	err := s.db.QueryRow(
		`SELECT id, name, root, created_at FROM workspaces WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Root, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get workspace %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get workspace %q: %w", id, err)
	}
	if w.CreatedAt, err = decodeTime(created); err != nil {
		return nil, fmt.Errorf("store: get workspace %q: %w", id, err)
	}
	return &w, nil
}

// RenameWorkspace changes a workspace's name and returns the updated row.
func (s *Store) RenameWorkspace(id, name string) (*Workspace, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: rename workspace: empty id", ErrInvalidArgument)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: rename workspace: empty name", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: rename workspace: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`UPDATE workspaces SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return nil, fmt.Errorf("store: rename workspace %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("store: rename workspace %q: %w", id, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("store: rename workspace %q: %w", id, ErrNotFound)
	}
	var (
		w       Workspace
		created string
	)
	if err := tx.QueryRow(
		`SELECT id, name, root, created_at FROM workspaces WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Root, &created); err != nil {
		return nil, fmt.Errorf("store: rename workspace %q: read back: %w", id, err)
	}
	if w.CreatedAt, err = decodeTime(created); err != nil {
		return nil, fmt.Errorf("store: rename workspace %q: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: rename workspace %q: commit: %w", id, err)
	}
	return &w, nil
}

// DeleteWorkspace removes a workspace and, through the ON DELETE CASCADE
// clauses, every session, message, event and tool call stored under it. It
// returns an error wrapping ErrNotFound when no such workspace exists.
func (s *Store) DeleteWorkspace(id string) error {
	if id == "" {
		return fmt.Errorf("%w: delete workspace: empty id", ErrInvalidArgument)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: delete workspace %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	// The transcript directories live outside SQLite, so capture the session
	// ids before the cascade removes the rows that name them.
	rows, qerr := tx.Query(`SELECT id FROM sessions WHERE workspace_id = ?`, id)
	if qerr != nil {
		return fmt.Errorf("store: delete workspace %q: list sessions: %w", id, qerr)
	}
	var sessionIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close() //nolint:errcheck // scan error wins
			return fmt.Errorf("store: delete workspace %q: scan session: %w", id, err)
		}
		sessionIDs = append(sessionIDs, sid)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck // rows error wins
		return fmt.Errorf("store: delete workspace %q: sessions: %w", id, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("store: delete workspace %q: close sessions: %w", id, err)
	}

	res, err := tx.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete workspace %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete workspace %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete workspace %q: %w", id, ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete workspace %q: commit: %w", id, err)
	}
	for _, sid := range sessionIDs {
		if err := s.RemoveSessionDir(sid); err != nil {
			return err
		}
		if err := s.RemoveAttachmentDir(sid); err != nil {
			return err
		}
	}
	return nil
}

// workspaceExists reports whether a workspace row with id exists.
func workspaceExists(tx *sql.Tx, id string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM workspaces WHERE id = ?`, id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check workspace %q: %w", id, err)
	}
	return n > 0, nil
}
