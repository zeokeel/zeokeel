package modelcatalog

// Tests for the one-time legacy-connection migration performed on module
// activation: an existing single-provider chat.* configuration becomes the first
// modelcatalog.models row so the multi-provider model list upgrade does not drop
// it. The migration must never overwrite a row the user saved and must never
// fail activation.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// recordingSettings records the rows written through Set and can fail writes on
// demand (activation's migration is best-effort).
type recordingSettings struct {
	*settingsStore

	mu     sync.Mutex
	writes []string
	value  string
	// setErr, when non-nil, makes every Set fail without storing the value.
	setErr error
}

func newRecordingSettings(values map[string]string) *recordingSettings {
	return &recordingSettings{settingsStore: newSettingsStore(values)}
}

func (s *recordingSettings) Set(key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	if err := s.settingsStore.Set(key, value); err != nil {
		return err
	}
	s.mu.Lock()
	s.writes = append(s.writes, key)
	s.value = value
	s.mu.Unlock()
	return nil
}

// writeCount returns the number of successful Set calls.
func (s *recordingSettings) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

// lastValue returns the value of the last successful Set call.
func (s *recordingSettings) lastValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// contains reports whether any recorded log line contains fragment.
func (l *fakeLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// activateCatalog loads the module against a fake host (logger, plus the given
// settings store when non-nil) and returns the provided catalog and its log.
func activateCatalog(t *testing.T, settings contracts.SettingsService,
	cfg map[string]any) (contracts.ModelCatalogService, *fakeLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if settings != nil {
		if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	entry := module.Entry{ID: "modelcatalog", Module: ModuleID}
	if cfg != nil {
		entry.Config = cfg
	}
	loader, err := module.Load(root, registry, []module.Entry{entry})
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	catalog, err := root.Require(contracts.KeyModelCatalog)
	if err != nil {
		t.Fatalf("require KeyModelCatalog: %v", err)
	}
	return catalog, logger
}

// TestActivationMigratesLegacyConnection covers the one-time upgrade: a
// configuration that predates the model list turns its global connection into
// one model row, and the legacy chat.* keys are left untouched.
func TestActivationMigratesLegacyConnection(t *testing.T) {
	settings := newRecordingSettings(map[string]string{
		contracts.SettingsChatModel:   "legacy-model",
		contracts.SettingsChatBaseURL: "https://legacy.example/v1/",
		contracts.SettingsChatAPIKey:  " legacy-key ",
	})
	catalog, logger := activateCatalog(t, settings, nil)

	models := catalog.Models()
	if len(models) != 1 || models[0].ID != "legacy-model" {
		t.Fatalf("migrated catalog = %+v, want one legacy-model row", models)
	}
	row := models[0]
	if row.Name != "legacy-model" || row.BaseURL != "https://legacy.example/v1" || row.APIKey != "legacy-key" {
		t.Fatalf("migrated row = %+v, want id/name, normalized URL and trimmed key", row)
	}
	if got := settings.writeCount(); got != 1 {
		t.Fatalf("settings writes = %d, want exactly the one migration write", got)
	}
	if key := settings.writes[0]; key != contracts.SettingModelCatalogModels {
		t.Fatalf("migration wrote %q, want %q", key, contracts.SettingModelCatalogModels)
	}
	// chat.model stays the default-model mirror; the migration never clears the
	// legacy rows.
	if got := settings.Get(contracts.SettingsChatModel, ""); got != "legacy-model" {
		t.Fatalf("chat.model = %q, want it kept", got)
	}
	if got := settings.Get(contracts.SettingsChatBaseURL, ""); got != "https://legacy.example/v1/" {
		t.Fatalf("chat.baseURL = %q, want it kept", got)
	}
	if got := settings.Get(contracts.SettingsChatAPIKey, ""); got != " legacy-key " {
		t.Fatalf("chat.apiKey = %q, want it kept", got)
	}
	if logger.count() != 0 {
		t.Fatalf("successful migration logged %d lines, want none", logger.count())
	}

	// A later activation sees the row and must not migrate again.
	catalog2, _ := activateCatalog(t, settings, nil)
	if again := catalog2.Models(); len(again) != 1 || again[0].ID != "legacy-model" {
		t.Fatalf("second activation catalog = %+v", again)
	}
	if got := settings.writeCount(); got != 1 {
		t.Fatalf("settings writes after second activation = %d, want 1 (idempotent)", got)
	}
}

// TestActivationKeepsExistingModelList covers the "never overwrite" rule: any
// present row — a user list, an explicit empty list, or malformed JSON — stops
// the migration.
func TestActivationKeepsExistingModelList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "user list", raw: `[{"id":"user-model","name":"User"}]`},
		{name: "explicit empty", raw: "[]"},
		{name: "malformed", raw: "{not-json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			settings := newRecordingSettings(map[string]string{
				contracts.SettingModelCatalogModels: testCase.raw,
				contracts.SettingsChatModel:         "legacy-model",
				contracts.SettingsChatBaseURL:       "https://legacy.example/v1",
			})
			catalog, _ := activateCatalog(t, settings, nil)
			if got := settings.Get(contracts.SettingModelCatalogModels, ""); got != testCase.raw {
				t.Fatalf("settings row changed: %q -> %q", testCase.raw, got)
			}
			if got := settings.writeCount(); got != 0 {
				t.Fatalf("settings writes = %d, want none for an existing row", got)
			}
			models := catalog.Models()
			switch testCase.name {
			case "user list":
				if len(models) != 1 || models[0].ID != "user-model" {
					t.Fatalf("catalog = %+v, want the user list", models)
				}
			default:
				if len(models) != 0 {
					t.Fatalf("catalog = %+v, want the empty baseline (no built-in models)", models)
				}
			}
		})
	}
}

// TestActivationMigrationWriteFailureIsBestEffort covers a read-only settings
// store: activation must succeed, the catalog keeps the baseline, and the failed
// write is logged.
func TestActivationMigrationWriteFailureIsBestEffort(t *testing.T) {
	settings := newRecordingSettings(map[string]string{
		contracts.SettingsChatModel: "legacy-model",
	})
	settings.setErr = errors.New("read-only settings")
	catalog, logger := activateCatalog(t, settings, nil)

	if models := catalog.Models(); len(models) != 0 {
		t.Fatalf("catalog after failed migration = %+v, want the empty baseline", models)
	}
	if !logger.contains("migrate legacy chat connection") {
		t.Fatalf("migration failure was not logged (lines=%v)", logger.lines)
	}
}

// TestActivationWithoutLegacyModelDoesNotWrite covers a store with no chat.model:
// there is nothing to migrate, so no row is written.
func TestActivationWithoutLegacyModelDoesNotWrite(t *testing.T) {
	settings := newRecordingSettings(map[string]string{
		contracts.SettingsChatBaseURL: "https://legacy.example/v1",
	})
	_, _ = activateCatalog(t, settings, nil)
	if got := settings.writeCount(); got != 0 {
		t.Fatalf("settings writes = %d, want none without chat.model", got)
	}
	if got := settings.Get(contracts.SettingModelCatalogModels, ""); got != "" {
		t.Fatalf("modelcatalog.models = %q, want it unset", got)
	}
}

// TestActivationWithoutSettingsStore covers a host with no settings service (the
// module keeps working off the assembly baseline and cannot migrate).
func TestActivationWithoutSettingsStore(t *testing.T) {
	cfg := map[string]any{"models": []any{map[string]any{"id": "baseline", "name": "Baseline"}}}
	catalog, logger := activateCatalog(t, nil, cfg)
	models := catalog.Models()
	if len(models) != 1 || models[0].ID != "baseline" {
		t.Fatalf("catalog without settings = %+v, want the assembly baseline", models)
	}
	if logger.count() != 0 {
		t.Fatalf("activation logged %d lines, want none", logger.count())
	}
}

// TestMigratedRowIsTheEffectiveList covers the post-migration read path: after
// activation the catalog list is the migrated row, so the agent resolves the
// legacy model's own connection from it.
func TestMigratedRowIsTheEffectiveList(t *testing.T) {
	settings := newRecordingSettings(map[string]string{
		contracts.SettingsChatModel:   "legacy-model",
		contracts.SettingsChatBaseURL: "https://legacy.example/v1",
		contracts.SettingsChatAPIKey:  "legacy-key",
	})
	catalog, _ := activateCatalog(t, settings, nil)
	encoded := settings.lastValue()
	if !strings.Contains(encoded, `"baseURL":"https://legacy.example/v1"`) {
		t.Fatalf("migrated row = %s, want the legacy base URL", encoded)
	}
	parsed, err := contracts.ParseModelInfoList(encoded)
	if err != nil {
		t.Fatalf("parse migrated row: %v", err)
	}
	baseURL, apiKey := contracts.ModelConnection(parsed, "legacy-model", "", "")
	if baseURL != "https://legacy.example/v1" || apiKey != "legacy-key" {
		t.Fatalf("resolved migrated connection = %q / %q", baseURL, apiKey)
	}
	if models := catalog.Models(); len(models) != 1 || models[0].ID != "legacy-model" {
		t.Fatalf("catalog = %+v", models)
	}
}
