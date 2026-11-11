package memory

// M-10: the configured consolidation policy (memory.maxEntries /
// memory.maxAgeDays) is enforced automatically on open and after every write,
// not only when the user clicks the settings button.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// policySettings is a minimal contracts.SettingsService for policy tests.
type policySettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *policySettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *policySettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *policySettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

func writeMemoryDocument(t *testing.T, path string, entries []contracts.Memory) {
	t.Helper()
	data, err := json.MarshalIndent(fileDocument{Entries: entries}, "", "  ")
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write memory document: %v", err)
	}
}

func TestAutomaticPolicyOnOpenTrimsExpiredAndOverCap(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, DefaultFileName)
	now := time.Now().UTC()
	writeMemoryDocument(t, path, []contracts.Memory{
		{ID: "expired", Text: "old", Scope: "global", Created: now.Add(-30 * 24 * time.Hour)},
		{ID: "old-2", Text: "second", Scope: "global", Created: now.Add(-2 * time.Hour)},
		{ID: "new-1", Text: "newest", Scope: "global", Created: now.Add(-time.Hour)},
	})
	logger := &testLogger{}
	store, err := openStore(dataDir, Config{}, logger, nil, contracts.MemoryPolicy{
		MaxEntries: 2,
		MaxAge:     7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	all, err := store.ListAll(context.Background())
	if err != nil || len(all) != 2 {
		t.Fatalf("entries after open = %+v/%v, want the two newest", all, err)
	}
	for _, memory := range all {
		if memory.ID == "expired" {
			t.Fatalf("expired entry survived the open policy: %+v", all)
		}
	}
	if !logger.contains("automatic memory consolidation") {
		t.Fatalf("policy run was not logged: %v", logger.lines)
	}
	// The trimmed document is persisted immediately.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	if strings.Contains(string(raw), `"expired"`) {
		t.Fatalf("expired entry still persisted: %s", raw)
	}
}

func TestAutomaticPolicyOnAddEnforcesCap(t *testing.T) {
	dataDir := t.TempDir()
	store, err := openStore(dataDir, Config{}, &testLogger{}, nil, contracts.MemoryPolicy{MaxEntries: 2})
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	ctx := context.Background()
	for index := 0; index < 4; index++ {
		if _, err := store.Add(ctx, contracts.Memory{Text: fmt.Sprintf("entry-%d", index)}); err != nil {
			t.Fatalf("Add %d: %v", index, err)
		}
	}
	all, err := store.ListAll(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("entries after 4 adds = %d/%v, want the cap of 2", len(all), err)
	}
	if all[0].Text != "entry-2" || all[1].Text != "entry-3" {
		t.Fatalf("kept entries = %+v, want the two newest", all)
	}
}

// TestModuleReadsPolicyFromSettings covers the wiring: the module reads the two
// settings rows and enforces them at activation.
func TestModuleReadsPolicyFromSettings(t *testing.T) {
	dataDir := t.TempDir()
	writeMemoryDocument(t, filepath.Join(dataDir, DefaultFileName), []contracts.Memory{
		{ID: "old", Text: "old", Scope: "global", Created: time.Now().UTC().Add(-40 * 24 * time.Hour)},
		{ID: "new", Text: "new", Scope: "global", Created: time.Now().UTC().Add(-time.Hour)},
	})
	settings := &policySettings{values: map[string]string{
		contracts.SettingMemoryMaxEntries: "1",
		contracts.SettingMemoryMaxAgeDays: "7",
	}}
	h := mustLoadMemory(t, dataDir, nil, func(ctx cordis.Context) {
		if _, err := ctx.Provide(contracts.KeySettings, contracts.SettingsService(settings)); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	})
	allLister, ok := h.store.(contracts.MemoryAllLister)
	if !ok {
		t.Fatalf("store does not implement ListAll: %T", h.store)
	}
	entries, err := allLister.ListAll(context.Background())
	if err != nil || len(entries) != 1 || entries[0].ID != "new" {
		t.Fatalf("entries after activation = %+v/%v, want only the new one", entries, err)
	}
	if !h.logger.contains("automatic memory consolidation") {
		t.Fatalf("automatic policy not logged: %v", h.logger.lines)
	}
}
