package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Model is one language-model configuration. The shell's Settings > Models
// page owns these values; the runtime reads the selected default through the
// provider.* settings kept in sync by SetDefaultModel.
//
// A model is stored as one settings row per field:
//
//	model.<id>.name
//	model.<id>.base_url
//	model.<id>.api_key
//	model.<id>.model
//	model.<id>.reasoning_effort
//	model.<id>.context_window
//	model.default            -- the selected model's id
type Model struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	// ContextWindow is the model's combined request/response capacity in
	// tokens, 0 when unknown. The runtime derives a session's compaction
	// budget from it; the Add model dialog fills it from the built-in
	// provider catalog when the user picks a published model.
	ContextWindow int `json:"context_window"`
	// Default is derived (it is not a stored field): true when the model is
	// the selected default.
	Default bool `json:"default"`
}

// Settings keys owned by the model store.
const (
	// ModelSettingPrefix prefixes every per-model settings key.
	ModelSettingPrefix = "model."
	// ModelDefaultKey names the selected default model's id.
	ModelDefaultKey = "model.default"
)

// Provider settings updated when the default model changes, so the next
// provider build (internal/app) uses the selected model.
const (
	ProviderBaseURLKey         = "provider.base_url"
	ProviderAPIKeyKey          = "provider.api_key"
	ProviderModelKey           = "provider.model"
	ProviderReasoningEffortKey = "provider.reasoning_effort"
)

// modelFields is the persisted field order used by create and update.
var modelFields = []string{"name", "base_url", "api_key", "model", "reasoning_effort", "context_window"}

// modelKey renders the settings key for one field of a model.
func modelKey(id, field string) string {
	return ModelSettingPrefix + id + "." + field
}

// validateModelID rejects ids that would make the key prefix ambiguous.
func validateModelID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: model id is empty", ErrInvalidArgument)
	}
	if strings.ContainsAny(id, ".%_") {
		return fmt.Errorf("%w: model id %q contains a reserved character", ErrInvalidArgument, id)
	}
	return nil
}

// ListModels returns every configured model, sorted by display name. Models
// with no name fall back to their model id for ordering.
func (s *Store) ListModels() ([]Model, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings WHERE key LIKE 'model.%'`)
	if err != nil {
		return nil, fmt.Errorf("store: list models: %w", err)
	}
	defer rows.Close()

	byID := make(map[string]*Model)
	var defaultID string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: list models: scan: %w", err)
		}
		if key == ModelDefaultKey {
			defaultID = value
			continue
		}
		id, field, ok := parseModelKey(key)
		if !ok {
			continue
		}
		m := byID[id]
		if m == nil {
			m = &Model{ID: id}
			byID[id] = m
		}
		switch field {
		case "name":
			m.Name = value
		case "base_url":
			m.BaseURL = value
		case "api_key":
			m.APIKey = value
		case "model":
			m.Model = value
		case "reasoning_effort":
			m.ReasoningEffort = value
		case "context_window":
			m.ContextWindow = parseContextWindow(value)
		}
	}

	list := make([]Model, 0, len(byID))
	for _, m := range byID {
		m.Default = m.ID == defaultID
		list = append(list, *m)
	}
	sort.Slice(list, func(i, j int) bool {
		li, lj := strings.ToLower(modelSortKey(list[i])), strings.ToLower(modelSortKey(list[j]))
		if li != lj {
			return li < lj
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// parseContextWindow reads a persisted token capacity. Malformed or
// non-positive values read as unknown (0) instead of failing the whole list:
// a hand-edited settings row must not hide every model.
func parseContextWindow(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// formatContextWindow renders ContextWindow for its settings key; unknown (0)
// writes no row so a cleared value does not linger.
func formatContextWindow(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// modelSortKey is the display string a model is sorted by.
func modelSortKey(m Model) string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	if strings.TrimSpace(m.Model) != "" {
		return m.Model
	}
	return m.ID
}

// parseModelKey splits "model.<id>.<field>" into its parts. The id may not
// contain a dot, which validateModelID enforces when rows are written.
func parseModelKey(key string) (id, field string, ok bool) {
	if !strings.HasPrefix(key, ModelSettingPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, ModelSettingPrefix)
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return "", "", false
	}
	id, field = rest[:dot], rest[dot+1:]
	switch field {
	case "name", "base_url", "api_key", "model", "reasoning_effort", "context_window":
		return id, field, true
	default:
		return "", "", false
	}
}

// GetModel returns the model with id, or an error wrapping ErrNotFound.
func (s *Store) GetModel(id string) (*Model, error) {
	if err := validateModelID(id); err != nil {
		return nil, fmt.Errorf("store: get model: %w", err)
	}
	rows, err := s.db.Query(
		`SELECT key, value FROM settings WHERE key LIKE ?`, ModelSettingPrefix+id+".%")
	if err != nil {
		return nil, fmt.Errorf("store: get model %q: %w", id, err)
	}
	defer rows.Close()

	m := &Model{ID: id}
	found := false
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: get model %q: scan: %w", id, err)
		}
		gotID, field, ok := parseModelKey(key)
		if !ok || gotID != id {
			continue
		}
		found = true
		switch field {
		case "name":
			m.Name = value
		case "base_url":
			m.BaseURL = value
		case "api_key":
			m.APIKey = value
		case "model":
			m.Model = value
		case "reasoning_effort":
			m.ReasoningEffort = value
		case "context_window":
			m.ContextWindow = parseContextWindow(value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get model %q: %w", id, err)
	}
	// Close before the next query: the store has a single writer connection,
	// so an open rows handle would deadlock Select.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: get model %q: close rows: %w", id, err)
	}
	if !found {
		return nil, fmt.Errorf("store: get model %q: %w", id, ErrNotFound)
	}
	if def, err := s.DefaultModelID(); err == nil {
		m.Default = def == id
	}
	return m, nil
}

// CreateModel inserts a model, generating an id when the input has none. The
// first model automatically becomes the default.
func (s *Store) CreateModel(m Model) (*Model, error) {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return nil, fmt.Errorf("%w: create model: empty name", ErrInvalidArgument)
	}
	if m.ContextWindow < 0 {
		return nil, fmt.Errorf("%w: create model: negative context window %d", ErrInvalidArgument, m.ContextWindow)
	}
	if m.ID == "" {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		m.ID = id
	}
	if err := validateModelID(m.ID); err != nil {
		return nil, fmt.Errorf("store: create model: %w", err)
	}
	if _, err := s.GetModel(m.ID); err == nil {
		return nil, fmt.Errorf("store: create model %q: already exists", m.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	hasDefault := true
	if _, err := s.DefaultModelID(); errors.Is(err, ErrNotFound) {
		hasDefault = false
	} else if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: create model: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if err := writeModelTx(tx, m); err != nil {
		return nil, err
	}
	if !hasDefault {
		if err := setDefaultTx(tx, m); err != nil {
			return nil, err
		}
		m.Default = true
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: create model: commit: %w", err)
	}
	return &m, nil
}

// UpdateModel replaces every field of an existing model. The default model's
// provider.* settings are refreshed so a later build sees the new values.
func (s *Store) UpdateModel(m Model) (*Model, error) {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return nil, fmt.Errorf("%w: update model: empty name", ErrInvalidArgument)
	}
	if m.ContextWindow < 0 {
		return nil, fmt.Errorf("%w: update model: negative context window %d", ErrInvalidArgument, m.ContextWindow)
	}
	if err := validateModelID(m.ID); err != nil {
		return nil, fmt.Errorf("store: update model: %w", err)
	}
	if _, err := s.GetModel(m.ID); err != nil {
		return nil, err
	}

	defaultID, err := s.DefaultModelID()
	isDefault := err == nil && defaultID == m.ID
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: update model: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if err := writeModelTx(tx, m); err != nil {
		return nil, err
	}
	if isDefault {
		if err := setDefaultTx(tx, m); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: update model: commit: %w", err)
	}
	m.Default = isDefault
	return &m, nil
}

// DeleteModel removes a model. Deleting the default clears the default and the
// provider.* settings, so the runtime falls back to environment/defaults.
func (s *Store) DeleteModel(id string) error {
	if err := validateModelID(id); err != nil {
		return fmt.Errorf("store: delete model: %w", err)
	}
	if _, err := s.GetModel(id); err != nil {
		return err
	}
	defaultID, err := s.DefaultModelID()
	isDefault := err == nil && defaultID == id
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: delete model %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	for _, field := range modelFields {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, modelKey(id, field)); err != nil {
			return fmt.Errorf("store: delete model %q: %w", id, err)
		}
	}
	if isDefault {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, ModelDefaultKey); err != nil {
			return fmt.Errorf("store: delete model %q: clear default: %w", id, err)
		}
		if err := clearProviderTx(tx); err != nil {
			return fmt.Errorf("store: delete model %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete model %q: commit: %w", id, err)
	}
	return nil
}

// DefaultModelID returns the selected default model's id, or ErrNotFound when
// no default is set.
func (s *Store) DefaultModelID() (string, error) {
	id, err := s.GetSetting(ModelDefaultKey)
	if errors.Is(err, ErrNotFound) {
		return "", fmt.Errorf("store: default model: %w", ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: default model: %w", err)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("store: default model: %w", ErrNotFound)
	}
	return id, nil
}

// DefaultModel returns the selected default model.
func (s *Store) DefaultModel() (*Model, error) {
	id, err := s.DefaultModelID()
	if err != nil {
		return nil, err
	}
	m, err := s.GetModel(id)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("store: default model %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	m.Default = true
	return m, nil
}

// SetDefaultModel selects id as the default and refreshes provider.*.
func (s *Store) SetDefaultModel(id string) error {
	if err := validateModelID(id); err != nil {
		return fmt.Errorf("store: set default model: %w", err)
	}
	m, err := s.GetModel(id)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: set default model %q: begin: %w", id, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if err := setDefaultTx(tx, *m); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set default model %q: commit: %w", id, err)
	}
	return nil
}

// writeModelTx writes every persisted field of m. Empty fields delete their
// key so a cleared value does not linger.
func writeModelTx(tx *sql.Tx, m Model) error {
	values := []struct{ field, value string }{
		{"name", m.Name},
		{"base_url", m.BaseURL},
		{"api_key", m.APIKey},
		{"model", m.Model},
		{"reasoning_effort", m.ReasoningEffort},
		{"context_window", formatContextWindow(m.ContextWindow)},
	}
	for _, v := range values {
		if err := writeSettingTx(tx, modelKey(m.ID, v.field), strings.TrimSpace(v.value)); err != nil {
			return fmt.Errorf("store: write model %q: %w", m.ID, err)
		}
	}
	return nil
}

// setDefaultTx records m as the default and syncs provider.* with its values.
func setDefaultTx(tx *sql.Tx, m Model) error {
	if err := writeSettingTx(tx, ModelDefaultKey, m.ID); err != nil {
		return fmt.Errorf("store: set default model: %w", err)
	}
	providerModel := strings.TrimSpace(m.Model)
	if providerModel == "" {
		providerModel = strings.TrimSpace(m.Name)
	}
	provider := []struct{ key, value string }{
		{ProviderBaseURLKey, strings.TrimSpace(m.BaseURL)},
		{ProviderAPIKeyKey, strings.TrimSpace(m.APIKey)},
		{ProviderModelKey, providerModel},
		{ProviderReasoningEffortKey, strings.TrimSpace(m.ReasoningEffort)},
	}
	for _, v := range provider {
		if err := writeSettingTx(tx, v.key, v.value); err != nil {
			return fmt.Errorf("store: set default model: %w", err)
		}
	}
	return nil
}

// clearProviderTx removes the provider.* settings written by a default model.
func clearProviderTx(tx *sql.Tx) error {
	for _, key := range []string{
		ProviderBaseURLKey, ProviderAPIKeyKey, ProviderModelKey, ProviderReasoningEffortKey,
	} {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
			return fmt.Errorf("clear %s: %w", key, err)
		}
	}
	return nil
}

// writeSettingTx upserts a setting, deleting the key for an empty value.
func writeSettingTx(tx *sql.Tx, key, value string) error {
	if value == "" {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
			return err
		}
		return nil
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return err
	}
	return nil
}

// ModelsJSON is a convenience for callers that want the whole model list as
// JSON (tests and export). It is not a persisted format.
func (s *Store) ModelsJSON() ([]byte, error) {
	list, err := s.ListModels()
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}
