package teamnotes_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/teamnotes"
)

// testLogger captures what the module logs while it boots.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, format)
}

func (l *testLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// TestDescriptorMetadata pins the seam: the board provides KeyTeamNotes, and
// its only dependency is the optional memory store (a product without memory
// must still boot).
func TestDescriptorMetadata(t *testing.T) {
	descriptor := teamnotes.Module()
	if descriptor.ID != teamnotes.ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, teamnotes.ModuleID)
	}
	provided := map[string]bool{}
	for _, provide := range descriptor.Provides {
		provided[provide.Name] = true
	}
	if !provided[contracts.KeyTeamNotes.Name()] || !provided[contracts.KeyTeamNoteSuggestions.Name()] {
		t.Fatalf("provides = %+v, want the notes and suggestion-queue keys", descriptor.Provides)
	}
	if len(descriptor.Provides) != 2 {
		t.Fatalf("provides = %d, want exactly two", len(descriptor.Provides))
	}
	if len(descriptor.Requires) != 1 {
		t.Fatalf("requires = %+v, want exactly the optional memory service", descriptor.Requires)
	}
	requirement := descriptor.Requires[0]
	if requirement.Name != contracts.KeyMemoryService.Name() {
		t.Fatalf("requires = %+v, want the memory service", descriptor.Requires)
	}
	if !requirement.Optional {
		t.Fatalf("memory service requirement is hard; a memory-less product could not boot")
	}
}

// loadModule activates the descriptor over an optional memory service and
// returns the service it provided.
func loadModule(t *testing.T, memory contracts.MemoryService) (contracts.TeamNotesService, *testLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	logger := &testLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if memory != nil {
		if _, err := root.Provide(contracts.KeyMemoryService, memory); err != nil {
			t.Fatalf("provide memory service: %v", err)
		}
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(teamnotes.Module()); err != nil {
		t.Fatalf("register teamnotes module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "teamnotes",
		Module: teamnotes.ModuleID,
		Config: map[string]any{"workspaceRoot": "/tmp/ws-module"},
	}})
	if err != nil {
		t.Fatalf("load teamnotes module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	instance, ok := loader.Instance("teamnotes")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("teamnotes instance = (%v, %v), want active", ok, instance.State())
	}
	service, ok := root.Get(contracts.KeyTeamNotes)
	if !ok || service == nil {
		t.Fatal("contracts.KeyTeamNotes was not provided by the module")
	}
	return service, logger
}

func TestModuleProvidesABoardOverTheMemoryService(t *testing.T) {
	memory := newFakeMemory()
	service, logger := loadModule(t, memory)

	recorded, err := service.Record(context.Background(), contracts.TeamNote{
		TaskID: "T-5",
		Title:  "module wiring",
		Body:   "recorded through the provided service",
		Author: "ann",
	})
	if err != nil {
		t.Fatalf("Record through the provided service: %v", err)
	}
	if recorded.ID != "mem://user/self/memories/team-note/t-5" {
		t.Fatalf("recorded ID = %q, want memory://team/t-5", recorded.ID)
	}
	if len(memory.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(memory.writes))
	}
	// The configured root reaches the memory service.
	notes, err := service.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List through the provided service: %v", err)
	}
	if len(notes) != 1 || notes[0].Title != "module wiring" {
		t.Fatalf("notes = %+v, want the recorded note", notes)
	}
	if memory.listRoot != "/tmp/ws-module" {
		t.Fatalf("list root = %q, want the config's workspaceRoot", memory.listRoot)
	}
	if strings.Contains(logger.joined(), "unavailable") {
		t.Fatalf("module warned about a missing memory service it had: %q", logger.joined())
	}
}

func TestModuleBootsWithoutMemoryAndSaysSo(t *testing.T) {
	service, logger := loadModule(t, nil)

	if _, err := service.Record(context.Background(), contracts.TeamNote{Title: "t"}); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("Record = %v, want ErrTeamNotesUnavailable", err)
	}
	if _, err := service.List(context.Background(), 10); !errors.Is(err, contracts.ErrTeamNotesUnavailable) {
		t.Fatalf("List = %v, want ErrTeamNotesUnavailable", err)
	}
	if !strings.Contains(logger.joined(), "unavailable") {
		t.Fatalf("module did not log the missing memory service: %q", logger.joined())
	}
}
