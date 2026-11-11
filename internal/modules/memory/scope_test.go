package memory

// FR-25 / M-04: the memory scope carries the workspace identity, List/Search
// filter by the current workspace, Config.Scope is the single default and the
// model-facing add/update paths validate scopes like the store does.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeWorkspace is a switchable contracts.WorkspaceService: only Current is
// meaningful for the memory store; every other method is a stub.
type fakeWorkspace struct {
	mu      sync.Mutex
	current contracts.Workspace
	open    bool
}

func (f *fakeWorkspace) switchTo(id, root string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = contracts.Workspace{ID: id, Root: root}
	f.open = true
}

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current, f.open, nil
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.open {
		return nil, nil
	}
	return []contracts.Workspace{f.current}, nil
}
func (f *fakeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Delete(context.Context, string) error {
	return errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, errors.New("fake workspace: not implemented")
}
func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, errors.New("fake workspace: not implemented")
}

func mustOpenScopedStore(t *testing.T, dataDir string, cfg Config, workspace contracts.WorkspaceService) *Store {
	t.Helper()
	store, err := openStore(dataDir, cfg, nil, workspace, contracts.MemoryPolicy{})
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	return store
}

func memoryTexts(entries []contracts.Memory) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Text)
	}
	return out
}

func TestWorkspaceScopeBindsAndFiltersByCurrentWorkspace(t *testing.T) {
	ws := &fakeWorkspace{}
	ws.switchTo("ws-a", "/work/a")
	store := mustOpenScopedStore(t, t.TempDir(), Config{}, ws)
	ctx := context.Background()

	workspaceEntry, err := store.Add(ctx, contracts.Memory{Text: "workspace note", Scope: "workspace"})
	if err != nil {
		t.Fatalf("Add(workspace): %v", err)
	}
	if workspaceEntry.Scope != "workspace:ws-a" {
		t.Fatalf("stored scope = %q, want workspace:ws-a", workspaceEntry.Scope)
	}
	if _, err := store.Add(ctx, contracts.Memory{Text: "global note", Scope: "global"}); err != nil {
		t.Fatalf("Add(global): %v", err)
	}

	entries, err := store.List(ctx)
	if err != nil || len(entries) != 2 {
		t.Fatalf("List in ws-a = %v/%v, want both", memoryTexts(entries), err)
	}

	ws.switchTo("ws-b", "/work/b")
	entries, err = store.List(ctx)
	if err != nil || len(entries) != 1 || entries[0].Text != "global note" {
		t.Fatalf("List in ws-b = %v/%v, want only the global entry", memoryTexts(entries), err)
	}
	if found, err := store.Search(ctx, "workspace note"); err != nil || len(found) != 0 {
		t.Fatalf("Search(workspace note) in ws-b = %v/%v, want hidden", memoryTexts(found), err)
	}
	all, err := store.ListAll(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAll = %v/%v, want both workspaces", memoryTexts(all), err)
	}

	ws.switchTo("ws-a", "/work/a")
	entries, err = store.List(ctx)
	if err != nil || len(entries) != 2 {
		t.Fatalf("List back in ws-a = %v/%v, want both", memoryTexts(entries), err)
	}
}

func TestConfigScopeIsTheDefaultForEmptyScope(t *testing.T) {
	ws := &fakeWorkspace{}
	ws.switchTo("ws-a", "/work/a")

	global := mustOpenScopedStore(t, t.TempDir(), Config{}, ws)
	added, err := global.Add(context.Background(), contracts.Memory{Text: "default global"})
	if err != nil || added.Scope != contracts.MemoryScopeGlobal {
		t.Fatalf("default Add = %+v/%v, want global scope", added, err)
	}

	scoped := mustOpenScopedStore(t, t.TempDir(), Config{Scope: "workspace"}, ws)
	added, err = scoped.Add(context.Background(), contracts.Memory{Text: "default workspace"})
	if err != nil || added.Scope != "workspace:ws-a" {
		t.Fatalf("Config.Scope=workspace Add = %+v/%v, want workspace:ws-a", added, err)
	}
	if entries, err := scoped.List(context.Background()); err != nil || len(entries) != 1 {
		t.Fatalf("List = %+v/%v", entries, err)
	}
	ws.switchTo("ws-b", "/work/b")
	if entries, err := scoped.List(context.Background()); err != nil || len(entries) != 0 {
		t.Fatalf("config-scoped entry visible in ws-b: %+v/%v", entries, err)
	}
}

func TestLegacyWorkspaceScopeStaysVisibleForCompatibility(t *testing.T) {
	dataDir := t.TempDir()
	legacy := `{"entries":[
		{"id":"mem-legacy","text":"legacy workspace memory","scope":"workspace","created":"2026-01-01T00:00:00Z"},
		{"id":"mem-team","text":"legacy team memory","scope":"team","created":"2026-01-01T00:00:00Z"},
		{"id":"mem-global","text":"global memory","scope":"global","created":"2026-01-01T00:00:00Z"}
	]}`
	if err := os.WriteFile(filepath.Join(dataDir, DefaultFileName), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy document: %v", err)
	}
	ws := &fakeWorkspace{}
	ws.switchTo("ws-b", "/work/b")
	store := mustOpenScopedStore(t, dataDir, Config{}, ws)
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List legacy: %v", err)
	}
	if got := strings.Join(memoryTexts(entries), "|"); got != "legacy workspace memory|legacy team memory|global memory" {
		t.Fatalf("legacy entries visible = %v, want all three (never hide existing data)", memoryTexts(entries))
	}
}

func TestAddAndUpdateValidateScopesConsistently(t *testing.T) {
	ws := &fakeWorkspace{}
	ws.switchTo("ws-a", "/work/a")
	store := mustOpenScopedStore(t, t.TempDir(), Config{}, ws)
	ctx := context.Background()

	if _, err := store.Add(ctx, contracts.Memory{Text: "bad", Scope: "TEAM"}); err == nil {
		t.Fatal("Add accepted an unknown scope; want the same validation Update uses")
	}
	added, err := store.Add(ctx, contracts.Memory{Text: "update me", Scope: "workspace"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.Update(ctx, added.ID, "updated", "TEAM"); err == nil {
		t.Fatal("Update accepted an unknown scope")
	}
	updated, err := store.Update(ctx, added.ID, "updated", "")
	if err != nil || updated.Scope != "workspace:ws-a" {
		t.Fatalf("Update(keep scope) = %+v/%v", updated, err)
	}
	ws.switchTo("ws-b", "/work/b")
	updated, err = store.Update(ctx, added.ID, "moved", "workspace")
	if err != nil || updated.Scope != "workspace:ws-b" {
		t.Fatalf("Update(workspace) = %+v/%v, want the current workspace identity", updated, err)
	}
}

func TestStoreVersionAdvancesOnEveryMutation(t *testing.T) {
	ws := &fakeWorkspace{}
	ws.switchTo("ws-a", "/work/a")
	store := mustOpenScopedStore(t, t.TempDir(), Config{}, ws)
	ctx := context.Background()
	if version := store.Version(); version != 0 {
		t.Fatalf("fresh store version = %d, want 0", version)
	}
	added, err := store.Add(ctx, contracts.Memory{Text: "one", Scope: "global"})
	if err != nil || store.Version() != 1 {
		t.Fatalf("Add version = %d/%v, want 1", store.Version(), err)
	}
	if _, err := store.Update(ctx, added.ID, "two", ""); err != nil || store.Version() != 2 {
		t.Fatalf("Update version = %d/%v, want 2", store.Version(), err)
	}
	if _, err := store.Add(ctx, contracts.Memory{Text: "bad", Scope: "TEAM"}); err == nil || store.Version() != 2 {
		t.Fatalf("failed Add changed version = %d (err=%v)", store.Version(), err)
	}
	if !store.Remove(ctx, added.ID) || store.Version() != 3 {
		t.Fatalf("Remove version = %d, want 3", store.Version())
	}
}
