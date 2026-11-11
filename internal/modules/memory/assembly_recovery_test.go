package memory_test

// FR-06 assembly-level regression: none of the auxiliary user-data files
// (memory.json, workspaces.json, automations.json) may stop the product from
// loading. A corrupt file is quarantined, a warning is logged and the module
// starts with an empty store.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// recoverySettings is a minimal in-memory SettingsService for the assembly.
type recoverySettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *recoverySettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *recoverySettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *recoverySettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

// recoveryLogger records format/args lines.
type recoveryLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recoveryLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *recoveryLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestCorruptDataFilesDoNotBrickAssembly(t *testing.T) {
	dataDir := t.TempDir()
	for _, name := range []string{"memory.json", "workspaces.json", "automations.json"} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte("{broken"), 0o600); err != nil {
			t.Fatalf("write corrupt %s: %v", name, err)
		}
	}

	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	var settings contracts.SettingsService = &recoverySettings{}
	if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	logger := &recoveryLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{Version: "test"})
	if err != nil {
		t.Fatalf("assembly failed to start with corrupt data files: %v\nlogged:\n%s", err, logger.joined())
	}
	defer func() { _ = loader.Close() }()

	for _, name := range []string{"memory.json", "workspaces.json", "automations.json"} {
		if _, statErr := os.Stat(filepath.Join(dataDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("corrupt %s still at the canonical path (err=%v)", name, statErr)
		}
		backups, globErr := filepath.Glob(filepath.Join(dataDir, name+".corrupt-*"))
		if globErr != nil || len(backups) != 1 {
			t.Fatalf("quarantine backups for %s = %v/%v, want exactly one", name, backups, globErr)
		}
	}
	logged := logger.joined()
	if count := strings.Count(logged, "corrupt"); count < 3 {
		t.Fatalf("expected a recovery warning per corrupt file, got %d:\n%s", count, logged)
	}

	store, ok := root.Get(contracts.KeyMemory)
	if !ok || store == nil {
		t.Fatal("memory store was not provided after recovery")
	}
	if entries, listErr := store.List(context.Background()); listErr != nil || len(entries) != 0 {
		t.Fatalf("memory after recovery = %+v/%v, want empty", entries, listErr)
	}
	if _, addErr := store.Add(context.Background(), contracts.Memory{Text: "after recovery"}); addErr != nil {
		t.Fatalf("memory Add after recovery: %v", addErr)
	}
}
