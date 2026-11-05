package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Automation is one persisted automation task (docs/PLAN.md §9.1): a prompt
// run on a cron schedule. LastRun is nil until the scheduler has fired it at
// least once. NextRun is nil for a task that has never been scheduled.
type Automation struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Prompt    string     `json:"prompt"`
	Cron      string     `json:"cron"`
	Enabled   bool       `json:"enabled"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	NextRun   *time.Time `json:"next_run,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

const automationColumns = `id, title, prompt, cron, enabled, last_run, next_run, created_at`

// CreateAutomation inserts an automation task. A zero ID is generated, a zero
// CreatedAt becomes now, and title plus cron are required. The cron expression
// itself is validated by internal/automation, not by the store.
func (s *Store) CreateAutomation(a Automation) (*Automation, error) {
	a.Title = strings.TrimSpace(a.Title)
	a.Cron = strings.TrimSpace(a.Cron)
	if a.Title == "" {
		return nil, fmt.Errorf("%w: create automation: empty title", ErrInvalidArgument)
	}
	if a.Cron == "" {
		return nil, fmt.Errorf("%w: create automation: empty cron", ErrInvalidArgument)
	}
	if a.ID == "" {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		a.ID = id
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = nowUTC()
	} else {
		a.CreatedAt = a.CreatedAt.UTC()
	}
	a.LastRun = utcTimePtr(a.LastRun)
	a.NextRun = utcTimePtr(a.NextRun)
	if !a.Enabled {
		// A disabled task has no pending run: keeping a schedule would show
		// and store a "next run" that can never fire.
		a.NextRun = nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create automation: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(
		`INSERT INTO automations(`+automationColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Title, a.Prompt, a.Cron, boolInt(a.Enabled),
		encodeTimePtr(a.LastRun), encodeTimePtr(a.NextRun), encodeTime(a.CreatedAt),
	); err != nil {
		return nil, fmt.Errorf("store: create automation %q: insert: %w", a.Title, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create automation %q: commit: %w", a.Title, err)
	}
	out := a
	return &out, nil
}

// GetAutomation returns the task with id, or an error wrapping ErrNotFound.
func (s *Store) GetAutomation(id string) (*Automation, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: get automation: empty id", ErrInvalidArgument)
	}
	a, err := scanAutomation(s.db.QueryRow(
		`SELECT `+automationColumns+` FROM automations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: get automation %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get automation %q: %w", id, err)
	}
	return a, nil
}

// ListAutomations returns every task ordered by creation time.
func (s *Store) ListAutomations() ([]Automation, error) {
	rows, err := s.db.Query(
		`SELECT ` + automationColumns + ` FROM automations ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list automations: %w", err)
	}
	defer rows.Close()

	list := make([]Automation, 0)
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list automations: scan: %w", err)
		}
		list = append(list, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list automations: %w", err)
	}
	return list, nil
}

// UpdateAutomation replaces every mutable column of a task. CreatedAt is kept
// from the stored row when the input leaves it zero.
func (s *Store) UpdateAutomation(a Automation) (*Automation, error) {
	if a.ID == "" {
		return nil, fmt.Errorf("%w: update automation: empty id", ErrInvalidArgument)
	}
	a.Title = strings.TrimSpace(a.Title)
	a.Cron = strings.TrimSpace(a.Cron)
	if a.Title == "" {
		return nil, fmt.Errorf("%w: update automation: empty title", ErrInvalidArgument)
	}
	if a.Cron == "" {
		return nil, fmt.Errorf("%w: update automation: empty cron", ErrInvalidArgument)
	}

	existing, err := s.GetAutomation(a.ID)
	if err != nil {
		return nil, err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = existing.CreatedAt
	}
	a.LastRun = utcTimePtr(a.LastRun)
	a.NextRun = utcTimePtr(a.NextRun)
	if !a.Enabled {
		// A disabled task has no pending run: keeping a schedule would show
		// and store a "next run" that can never fire.
		a.NextRun = nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: update automation: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(
		`UPDATE automations
		    SET title = ?, prompt = ?, cron = ?, enabled = ?,
		        last_run = ?, next_run = ?, created_at = ?
		  WHERE id = ?`,
		a.Title, a.Prompt, a.Cron, boolInt(a.Enabled),
		encodeTimePtr(a.LastRun), encodeTimePtr(a.NextRun), encodeTime(a.CreatedAt), a.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: update automation %q: %w", a.ID, err)
	}
	if err := requireRow(res, "update automation", a.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: update automation %q: commit: %w", a.ID, err)
	}
	out := a
	return &out, nil
}

// DeleteAutomation removes the task with id.
func (s *Store) DeleteAutomation(id string) error {
	if id == "" {
		return fmt.Errorf("%w: delete automation: empty id", ErrInvalidArgument)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: delete automation %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`DELETE FROM automations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete automation %q: %w", id, err)
	}
	if err := requireRow(res, "delete automation", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete automation %q: commit: %w", id, err)
	}
	return nil
}

// SetAutomationEnabled flips the enabled flag and returns the updated task.
func (s *Store) SetAutomationEnabled(id string, enabled bool) (*Automation, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: enable automation: empty id", ErrInvalidArgument)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: enable automation %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`UPDATE automations SET enabled = ? WHERE id = ?`, boolInt(enabled), id)
	if err != nil {
		return nil, fmt.Errorf("store: enable automation %q: %w", id, err)
	}
	if err := requireRow(res, "enable automation", id); err != nil {
		return nil, err
	}
	a, err := scanAutomation(tx.QueryRow(
		`SELECT `+automationColumns+` FROM automations WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("store: enable automation %q: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: enable automation %q: commit: %w", id, err)
	}
	return a, nil
}

// MarkAutomationRun records that the scheduler fired a task at lastRun and
// computed nextRun (nil clears it). It returns the updated task.
func (s *Store) MarkAutomationRun(id string, lastRun time.Time, nextRun *time.Time) (*Automation, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: mark automation run: empty id", ErrInvalidArgument)
	}
	at := lastRun.UTC()
	next := utcTimePtr(nextRun)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: mark automation %q run: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(
		`UPDATE automations SET last_run = ?, next_run = ? WHERE id = ?`,
		encodeTime(at), encodeTimePtr(next), id,
	)
	if err != nil {
		return nil, fmt.Errorf("store: mark automation %q run: %w", id, err)
	}
	if err := requireRow(res, "mark automation run", id); err != nil {
		return nil, err
	}
	a, err := scanAutomation(tx.QueryRow(
		`SELECT `+automationColumns+` FROM automations WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("store: mark automation %q run: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: mark automation %q run: commit: %w", id, err)
	}
	return a, nil
}

// SetAutomationNextRun stores the task's next scheduled instant.
func (s *Store) SetAutomationNextRun(id string, next time.Time) (*Automation, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: schedule automation: empty id", ErrInvalidArgument)
	}
	at := next.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: schedule automation %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	res, err := tx.Exec(`UPDATE automations SET next_run = ? WHERE id = ?`, encodeTime(at), id)
	if err != nil {
		return nil, fmt.Errorf("store: schedule automation %q: %w", id, err)
	}
	if err := requireRow(res, "schedule automation", id); err != nil {
		return nil, err
	}
	a, err := scanAutomation(tx.QueryRow(
		`SELECT `+automationColumns+` FROM automations WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("store: schedule automation %q: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: schedule automation %q: commit: %w", id, err)
	}
	return a, nil
}

// scanAutomation reads one row of automationColumns.
func scanAutomation(sc scanner) (*Automation, error) {
	var (
		a       Automation
		enabled int
		last    sql.NullString
		next    sql.NullString
		created string
	)
	if err := sc.Scan(&a.ID, &a.Title, &a.Prompt, &a.Cron, &enabled,
		&last, &next, &created); err != nil {
		return nil, err
	}
	a.Enabled = enabled != 0
	var err error
	if a.LastRun, err = decodeTimePtr(last); err != nil {
		return nil, fmt.Errorf("automation %q: last_run: %w", a.ID, err)
	}
	if a.NextRun, err = decodeTimePtr(next); err != nil {
		return nil, fmt.Errorf("automation %q: next_run: %w", a.ID, err)
	}
	if a.CreatedAt, err = decodeTime(created); err != nil {
		return nil, fmt.Errorf("automation %q: created_at: %w", a.ID, err)
	}
	return &a, nil
}

// requireRow turns a zero-row result into an ErrNotFound error.
func requireRow(res sql.Result, action, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s %q: rows affected: %w", action, id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: %s %q: %w", action, id, ErrNotFound)
	}
	return nil
}

// boolInt renders a bool for SQLite's INTEGER NOT NULL columns.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// utcTimePtr normalizes a nullable timestamp to UTC.
func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// encodeTimePtr renders a nullable timestamp for a TEXT column.
func encodeTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return encodeTime(*t)
}

// decodeTimePtr parses a nullable timestamp column.
func decodeTimePtr(v sql.NullString) (*time.Time, error) {
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	t, err := decodeTime(v.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
