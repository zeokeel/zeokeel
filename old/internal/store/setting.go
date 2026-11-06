package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetSetting returns the value stored for key, or an error wrapping
// ErrNotFound when the key has never been set.
func (s *Store) GetSetting(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: get setting: empty key", ErrInvalidArgument)
	}
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: get setting %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting inserts or replaces the value for key.
func (s *Store) SetSetting(key, value string) error {
	if key == "" {
		return fmt.Errorf("%w: set setting: empty key", ErrInvalidArgument)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: set setting %q: begin: %w", key, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set setting %q: commit: %w", key, err)
	}
	return nil
}
