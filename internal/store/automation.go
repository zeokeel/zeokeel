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
//
// The PD-06 binding fields scope an automation run: WorkspaceID chooses the
// workspace the run's session is created in (empty = current/default),
// Permission and Model are the session's access mode and model id (empty = the
// app defaults), and OriginSessionID records the session whose model created
// the task (0 for tasks created outside a session or before the column
// existed).
type Automation struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Prompt          string     `json:"prompt"`
	Cron            string     `json:"cron"`
	Enabled         bool       `json:"enabled"`
	WorkspaceID     string     `json:"workspace_id,omitempty"`
	Permission      string     `json:"permission,omitempty"`
	Model           string     `json:"model,omitempty"`
	OriginSessionID string     `json:"origin_session_id,omitempty"`
	LastRun         *time.Time `json:"last_run,omitempty"`
	NextRun         *time.Time `json:"next_run,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

const automationColumns = `id, title, prompt, cron, enabled, last_run, next_run, created_at, workspace_id, permission, model, origin_session_id`

// Automation run statuses. A run is created as running and finished with one of
// the terminal statuses; stopped means the turn was cancelled.
const (
	AutomationRunRunning   = "running"
	AutomationRunCompleted = "completed"
	AutomationRunFailed    = "failed"
	AutomationRunStopped   = "stopped"
)

// AutomationRun is one recorded execution of an automation task. StartedAt is
// when the scheduler fired the task; FinishedAt is nil while the turn is still
// running (or when the process died mid-run). SessionID is the session the run
// created, when one could be created.
type AutomationRun struct {
	ID           string     `json:"id"`
	AutomationID string     `json:"automation_id"`
	SessionID    string     `json:"session_id,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
}

const automationRunColumns = `id, automation_id, session_id, started_at, finished_at, status, error`

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
	a.WorkspaceID = strings.TrimSpace(a.WorkspaceID)
	a.Permission = strings.TrimSpace(a.Permission)
	a.Model = strings.TrimSpace(a.Model)
	a.OriginSessionID = strings.TrimSpace(a.OriginSessionID)
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
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Title, a.Prompt, a.Cron, boolInt(a.Enabled),
		encodeTimePtr(a.LastRun), encodeTimePtr(a.NextRun), encodeTime(a.CreatedAt),
		a.WorkspaceID, a.Permission, a.Model, a.OriginSessionID,
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
	a.WorkspaceID = strings.TrimSpace(a.WorkspaceID)
	a.Permission = strings.TrimSpace(a.Permission)
	a.Model = strings.TrimSpace(a.Model)
	a.OriginSessionID = strings.TrimSpace(a.OriginSessionID)
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
		        last_run = ?, next_run = ?, created_at = ?,
		        workspace_id = ?, permission = ?, model = ?, origin_session_id = ?
		  WHERE id = ?`,
		a.Title, a.Prompt, a.Cron, boolInt(a.Enabled),
		encodeTimePtr(a.LastRun), encodeTimePtr(a.NextRun), encodeTime(a.CreatedAt),
		a.WorkspaceID, a.Permission, a.Model, a.OriginSessionID, a.ID,
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
		&last, &next, &created,
		&a.WorkspaceID, &a.Permission, &a.Model, &a.OriginSessionID); err != nil {
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

// CreateAutomationRun records the start of one automation execution. The run
// starts as AutomationRunRunning and is finished with FinishAutomationRun.
func (s *Store) CreateAutomationRun(automationID, sessionID string) (*AutomationRun, error) {
	automationID = strings.TrimSpace(automationID)
	if automationID == "" {
		return nil, fmt.Errorf("%w: create automation run: empty automation id", ErrInvalidArgument)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	run := AutomationRun{
		ID:           id,
		AutomationID: automationID,
		SessionID:    strings.TrimSpace(sessionID),
		StartedAt:    nowUTC(),
		Status:       AutomationRunRunning,
	}
	if _, err := s.db.Exec(
		`INSERT INTO automation_runs(`+automationRunColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.AutomationID, run.SessionID, encodeTime(run.StartedAt),
		nil, run.Status, "",
	); err != nil {
		return nil, fmt.Errorf("store: create automation run for %q: %w", automationID, err)
	}
	out := run
	return &out, nil
}

// FinishAutomationRun marks a run terminal with status and errText. An unknown
// status is rejected; a run that is already finished keeps its first terminal
// result (idempotent retries do not rewrite history).
func (s *Store) FinishAutomationRun(id, status, errText string) (*AutomationRun, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: finish automation run: empty id", ErrInvalidArgument)
	}
	status = strings.TrimSpace(status)
	switch status {
	case AutomationRunCompleted, AutomationRunFailed, AutomationRunStopped:
	default:
		return nil, fmt.Errorf("%w: finish automation run: unknown status %q", ErrInvalidArgument, status)
	}
	finished := nowUTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: finish automation run %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(
		`UPDATE automation_runs
		    SET status = ?, error = ?, finished_at = ?
		  WHERE id = ? AND finished_at IS NULL`,
		status, strings.TrimSpace(errText), encodeTime(finished), id,
	); err != nil {
		return nil, fmt.Errorf("store: finish automation run %q: %w", id, err)
	}
	run, err := scanAutomationRun(tx.QueryRow(
		`SELECT `+automationRunColumns+` FROM automation_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: finish automation run %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: finish automation run %q: read back: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: finish automation run %q: commit: %w", id, err)
	}
	return run, nil
}

// ListAutomationRuns returns the newest runs of one automation, newest first.
// limit <= 0 defaults to 20 and is capped at 200.
func (s *Store) ListAutomationRuns(automationID string, limit int) ([]AutomationRun, error) {
	automationID = strings.TrimSpace(automationID)
	if automationID == "" {
		return nil, fmt.Errorf("%w: list automation runs: empty automation id", ErrInvalidArgument)
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT `+automationRunColumns+` FROM automation_runs
		  WHERE automation_id = ?
		  ORDER BY started_at DESC, id DESC
		  LIMIT ?`, automationID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list automation runs for %q: %w", automationID, err)
	}
	defer rows.Close()
	list := make([]AutomationRun, 0)
	for rows.Next() {
		run, err := scanAutomationRun(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list automation runs for %q: scan: %w", automationID, err)
		}
		list = append(list, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list automation runs for %q: %w", automationID, err)
	}
	return list, nil
}

// LatestAutomationRun returns the newest run of one automation, or an error
// wrapping ErrNotFound when it never ran.
func (s *Store) LatestAutomationRun(automationID string) (*AutomationRun, error) {
	automationID = strings.TrimSpace(automationID)
	if automationID == "" {
		return nil, fmt.Errorf("%w: latest automation run: empty automation id", ErrInvalidArgument)
	}
	run, err := scanAutomationRun(s.db.QueryRow(
		`SELECT `+automationRunColumns+` FROM automation_runs
		  WHERE automation_id = ?
		  ORDER BY started_at DESC, id DESC
		  LIMIT 1`, automationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: latest automation run for %q: %w", automationID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: latest automation run for %q: %w", automationID, err)
	}
	return run, nil
}

// scanAutomationRun reads one row of automationRunColumns.
func scanAutomationRun(sc scanner) (*AutomationRun, error) {
	var (
		run      AutomationRun
		started  string
		finished sql.NullString
	)
	if err := sc.Scan(&run.ID, &run.AutomationID, &run.SessionID, &started,
		&finished, &run.Status, &run.Error); err != nil {
		return nil, err
	}
	var err error
	if run.StartedAt, err = decodeTime(started); err != nil {
		return nil, fmt.Errorf("automation run %q: started_at: %w", run.ID, err)
	}
	if run.FinishedAt, err = decodeTimePtr(finished); err != nil {
		return nil, fmt.Errorf("automation run %q: finished_at: %w", run.ID, err)
	}
	return &run, nil
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
