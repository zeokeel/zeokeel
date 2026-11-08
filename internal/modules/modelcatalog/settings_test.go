package modelcatalog

// TDD tests (RED first) for the modelcatalog settings override: Models() reads
// contracts.SettingModelCatalogModels on every call, falling back to the
// configured/default presets for empty or malformed values.

import (
	"reflect"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// settingsStore is a mutex-protected contracts.SettingsService map.
type settingsStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newSettingsStore(values map[string]string) *settingsStore {
	if values == nil {
		values = map[string]string{}
	}
	return &settingsStore{values: values}
}

func (s *settingsStore) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *settingsStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *settingsStore) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

func (s *settingsStore) setModels(raw string) {
	_ = s.Set(contracts.SettingModelCatalogModels, raw)
}

// count returns the number of log lines recorded so far.
func (l *fakeLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func modelService(t *testing.T, cfg Config, settings *settingsStore) (*service, *fakeLogger) {
	t.Helper()
	logger := &fakeLogger{}
	return newServiceWithSettings(cfg, logger, settings), logger
}

// --- tests ------------------------------------------------------------------

func TestSettingsOverrideModels(t *testing.T) {
	store := newSettingsStore(nil)
	store.setModels(`[{"id":"user-a","name":"User A","note":"custom"},{"id":"user-b"}]`)
	svc, _ := modelService(t, Config{}, store)

	models := svc.Models()
	if len(models) != 2 {
		t.Fatalf("models = %+v, want the two overridden models", models)
	}
	if !reflect.DeepEqual(models[0], contracts.ModelInfo{ID: "user-a", Name: "User A", Note: "custom"}) {
		t.Fatalf("first override = %+v", models[0])
	}
	// A model without a name falls back to its id.
	if !reflect.DeepEqual(models[1], contracts.ModelInfo{ID: "user-b", Name: "user-b"}) {
		t.Fatalf("second override = %+v", models[1])
	}

	// The override wins over the assembly config too.
	cfg := Config{Models: []Model{{ID: "configured", Name: "Configured"}}}
	svc2, _ := modelService(t, cfg, store)
	if got := svc2.Models(); len(got) != 2 || got[0].ID != "user-a" {
		t.Fatalf("override did not win over config: %+v", got)
	}
}

func TestSettingsEmptyFallsBackToConfigured(t *testing.T) {
	svc, _ := modelService(t, Config{}, newSettingsStore(nil))
	models := svc.Models()
	if len(models) != 2 || models[0].ID != "deepseek-chat" {
		t.Fatalf("default fallback = %+v, want the built-in presets", models)
	}

	configured := Config{Models: []Model{{ID: "only", Name: "Only"}}}
	svc2, _ := modelService(t, configured, newSettingsStore(nil))
	if got := svc2.Models(); len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("config fallback = %+v, want the configured model", got)
	}
}

func TestSettingsEmptyArrayFallsBackToDefaults(t *testing.T) {
	// Locked semantics: an empty array means "not configured" → defaults.
	store := newSettingsStore(nil)
	store.setModels("[]")
	svc, _ := modelService(t, Config{}, store)
	if got := svc.Models(); len(got) != 2 || got[0].ID != "deepseek-chat" {
		t.Fatalf("empty-array fallback = %+v, want defaults", got)
	}
}

func TestSettingsBadJSONFallsBackAndWarnsOnce(t *testing.T) {
	store := newSettingsStore(nil)
	store.setModels("{not-json")
	svc, logger := modelService(t, Config{}, store)

	for attempt := 0; attempt < 3; attempt++ {
		models := svc.Models()
		if len(models) != 2 || models[0].ID != "deepseek-chat" {
			t.Fatalf("bad-JSON fallback = %+v, want defaults", models)
		}
	}
	if got := logger.count(); got != 1 {
		t.Fatalf("logger warnings = %d, want exactly 1 for repeated bad JSON", got)
	}
}

func TestSettingsSanitizesOverride(t *testing.T) {
	store := newSettingsStore(nil)
	store.setModels(`[
		{"id":"  a  ","name":"  A  ","note":"  n  "},
		{"id":"   "},
		{"id":"a","name":"duplicate"},
		{"id":"b","name":""},
		{"id":" c "}
	]`)
	svc, _ := modelService(t, Config{}, store)
	models := svc.Models()
	if len(models) != 3 {
		t.Fatalf("sanitized models = %+v, want a/b/c", models)
	}
	if !reflect.DeepEqual(models[0], contracts.ModelInfo{ID: "a", Name: "A", Note: "n"}) {
		t.Fatalf("trimmed first = %+v", models[0])
	}
	if models[1].ID != "b" || models[1].Name != "b" {
		t.Fatalf("name fallback = %+v", models[1])
	}
	if !reflect.DeepEqual(models[2], contracts.ModelInfo{ID: "c", Name: "c"}) {
		t.Fatalf("trimmed last = %+v", models[2])
	}
}

func TestSettingsModelsReturnsCopy(t *testing.T) {
	store := newSettingsStore(nil)
	store.setModels(`[{"id":"a","name":"A"}]`)
	svc, _ := modelService(t, Config{}, store)
	models := svc.Models()
	models[0].ID = "mutated"
	if again := svc.Models(); again[0].ID != "a" {
		t.Fatalf("override mutated through Models(): %+v", again)
	}
}

func TestSettingsNilStoreUsesConfig(t *testing.T) {
	logger := &fakeLogger{}
	svc := newServiceWithSettings(Config{Models: []Model{{ID: "only"}}}, logger, nil)
	if got := svc.Models(); len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("nil-store models = %+v, want the configured model", got)
	}
}

func TestSettingsConcurrentReads(t *testing.T) {
	store := newSettingsStore(nil)
	store.setModels(`[{"id":"a","name":"A"}]`)
	svc, _ := modelService(t, Config{}, store)

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 50; index++ {
				if worker%2 == 0 {
					store.setModels(`[{"id":"a","name":"A"}]`)
				} else {
					store.setModels(`[{"id":"b","name":"B"},{"id":"c"}]`)
				}
				if models := svc.Models(); len(models) == 0 {
					t.Errorf("Models() returned an empty catalog")
					return
				}
			}
		}()
	}
	wg.Wait()
}
