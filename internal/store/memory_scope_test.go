package store

import (
	"strings"
	"testing"
)

// TestMemoryScopedCreateListIsolation pins the two-scope model: a global memory
// has no workspace and is visible across workspaces; workspace memories stay in
// their workspace; status filters separate active from pending suggestions.
func TestMemoryScopedCreateListIsolation(t *testing.T) {
	s := openTestStore(t)
	wsA := mustWorkspace(t, s, "a", t.TempDir())
	wsB := mustWorkspace(t, s, "b", t.TempDir())

	global, err := s.CreateMemoryScoped(Memory{
		Scope:   MemoryScopeGlobal,
		Kind:    MemoryKindUser,
		Content: "Prefer concise answers with the conclusion first.",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped(global): %v", err)
	}
	if global.WorkspaceID != "" {
		t.Fatalf("global memory workspace_id = %q, want empty", global.WorkspaceID)
	}
	if global.Status != MemoryStatusActive || global.Source != MemorySourceManual {
		t.Fatalf("global defaults status/source = %q/%q, want active/manual", global.Status, global.Source)
	}
	if global.ContentHash == "" {
		t.Fatal("global memory has no content hash")
	}

	wsMem, err := s.CreateMemoryScoped(Memory{
		Scope:       MemoryScopeWorkspace,
		WorkspaceID: wsA.ID,
		Kind:        MemoryKindProject,
		Source:      MemorySourceAuto,
		Content:     "Run make lint before committing.",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped(workspace): %v", err)
	}
	if wsMem.WorkspaceID != wsA.ID {
		t.Fatalf("workspace memory workspace_id = %q, want %q", wsMem.WorkspaceID, wsA.ID)
	}

	pending, err := s.CreateMemoryScoped(Memory{
		Scope:       MemoryScopeWorkspace,
		WorkspaceID: wsA.ID,
		Content:     "Maybe the release branch is release/2026.",
		Status:      MemoryStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped(pending): %v", err)
	}

	globals, err := s.ListMemoriesScoped(MemoryScopeGlobal, "", "", 10)
	if err != nil {
		t.Fatalf("ListMemoriesScoped(global): %v", err)
	}
	if len(globals) != 1 || globals[0].ID != global.ID {
		t.Fatalf("global memories = %+v, want exactly the global memory", globals)
	}

	activeA, err := s.ListMemoriesScoped(MemoryScopeWorkspace, wsA.ID, MemoryStatusActive, 10)
	if err != nil {
		t.Fatalf("ListMemoriesScoped(active wsA): %v", err)
	}
	if len(activeA) != 1 || activeA[0].ID != wsMem.ID {
		t.Fatalf("active wsA = %+v, want exactly the workspace memory", activeA)
	}

	allA, err := s.ListMemoriesScoped(MemoryScopeWorkspace, wsA.ID, "", 10)
	if err != nil {
		t.Fatalf("ListMemoriesScoped(all wsA): %v", err)
	}
	if len(allA) != 2 {
		t.Fatalf("all wsA = %d, want active + pending = 2", len(allA))
	}
	if !containsMemoryID(allA, pending.ID) {
		t.Fatalf("pending suggestion %q missing from unfiltered workspace list", pending.ID)
	}

	if got, err := s.ListMemoriesScoped(MemoryScopeWorkspace, wsB.ID, "", 10); err != nil {
		t.Fatalf("ListMemoriesScoped(wsB): %v", err)
	} else if len(got) != 0 {
		t.Fatalf("wsB sees %d memories, want 0 (workspace isolation)", len(got))
	}

	if got, err := s.SearchMemoriesScoped(MemoryScopeWorkspace, wsA.ID, "make lint", MemoryStatusActive, 10); err != nil {
		t.Fatalf("SearchMemoriesScoped: %v", err)
	} else if len(got) != 1 || got[0].ID != wsMem.ID {
		t.Fatalf("search results = %+v, want the workspace memory", got)
	}
	if got, err := s.SearchMemoriesScoped(MemoryScopeGlobal, "", "conclusion", MemoryStatusActive, 10); err != nil {
		t.Fatalf("SearchMemoriesScoped(global): %v", err)
	} else if len(got) != 1 || got[0].ID != global.ID {
		t.Fatalf("global search results = %+v, want the global memory", got)
	}
}

func containsMemoryID(list []Memory, id string) bool {
	for _, m := range list {
		if m.ID == id {
			return true
		}
	}
	return false
}

// TestMemoryScopedValidation pins scope/workspace/status/content validation.
func TestMemoryScopedValidation(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "v", t.TempDir())

	cases := []struct {
		name string
		m    Memory
	}{
		{"global-with-workspace", Memory{Scope: MemoryScopeGlobal, WorkspaceID: ws.ID, Content: "x"}},
		{"workspace-without-workspace", Memory{Scope: MemoryScopeWorkspace, Content: "x"}},
		{"unknown-scope", Memory{Scope: "session", WorkspaceID: ws.ID, Content: "x"}},
		{"unknown-status", Memory{Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "x", Status: "archived"}},
		{"unknown-kind", Memory{Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "x", Kind: "todo"}},
		{"unknown-source", Memory{Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "x", Source: "extracted"}},
		{"empty-content", Memory{Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "   "}},
		{"oversized-content", Memory{Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: strings.Repeat("x", MaxMemoryContentBytes+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateMemoryScoped(tc.m); err == nil {
				t.Fatalf("CreateMemoryScoped(%+v) returned no error", tc.m)
			}
		})
	}

	if _, err := s.SetMemoryStatus("missing-id", MemoryStatusActive); err == nil {
		t.Fatal("SetMemoryStatus(unknown id) returned no error")
	}
	created, err := s.CreateMemoryScoped(Memory{Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "status check"})
	if err != nil {
		t.Fatalf("CreateMemoryScoped: %v", err)
	}
	if _, err := s.SetMemoryStatus(created.ID, "archived"); err == nil {
		t.Fatal("SetMemoryStatus(unknown status) returned no error")
	}
}

// TestMemoryLegacyAPIsStayWorkspaceActive pins backward compatibility: the old
// workspace API only surfaces active workspace memories, and old CreateMemory
// defaults to workspace/manual/active/project.
func TestMemoryLegacyAPIsStayWorkspaceActive(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "legacy", t.TempDir())

	old, err := s.CreateMemory(ws.ID, "Legacy workspace note.")
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	if old.Scope != MemoryScopeWorkspace || old.Kind != MemoryKindProject ||
		old.Source != MemorySourceManual || old.Status != MemoryStatusActive {
		t.Fatalf("legacy defaults scope/kind/source/status = %q/%q/%q/%q, want workspace/project/manual/active",
			old.Scope, old.Kind, old.Source, old.Status)
	}
	if _, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID,
		Content: "Pending suggestion.", Status: MemoryStatusPending,
	}); err != nil {
		t.Fatalf("CreateMemoryScoped(pending): %v", err)
	}

	list, err := s.ListMemories(ws.ID, 10)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(list) != 1 || list[0].ID != old.ID {
		t.Fatalf("ListMemories = %+v, want only the active memory", list)
	}
	search, err := s.SearchMemories(ws.ID, "Legacy")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(search) != 1 || search[0].ID != old.ID {
		t.Fatalf("SearchMemories = %+v, want only the active memory", search)
	}
	if _, err := s.UpdateMemory(old.ID, "Updated legacy note."); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	updated, err := s.GetMemory(old.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if updated.Content != "Updated legacy note." || updated.ContentHash == "" {
		t.Fatalf("updated memory = %+v, want new content and hash", updated)
	}
}

// TestMemoryScopedDedupeStatusCountsAndUsage pins exact-hash dedupe, status
// transitions, counts and last_used_at bookkeeping used by the prompt budget
// and the review inbox.
func TestMemoryScopedDedupeStatusCountsAndUsage(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "counts", t.TempDir())

	first, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "Use pnpm, not npm.",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped first: %v", err)
	}
	dup, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "Use pnpm, not npm.",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped duplicate: %v", err)
	}
	if dup.ID != first.ID {
		t.Fatalf("duplicate content created id %q, want the existing %q", dup.ID, first.ID)
	}
	all, err := s.ListMemoriesScoped(MemoryScopeWorkspace, ws.ID, "", 10)
	if err != nil {
		t.Fatalf("ListMemoriesScoped: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("memories after duplicate = %d, want 1", len(all))
	}

	found, err := s.FindMemoryByHash(MemoryScopeWorkspace, ws.ID, first.ContentHash)
	if err != nil {
		t.Fatalf("FindMemoryByHash: %v", err)
	}
	if found == nil || found.ID != first.ID {
		t.Fatalf("FindMemoryByHash = %+v, want %q", found, first.ID)
	}
	if none, err := s.FindMemoryByHash(MemoryScopeGlobal, "", first.ContentHash); err != nil || none != nil {
		t.Fatalf("FindMemoryByHash(global) = %+v, %v; want nil, nil", none, err)
	}

	pending, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeGlobal, Content: "Prefer tables over prose.",
		Status: MemoryStatusPending, Source: MemorySourceAuto,
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped pending global: %v", err)
	}
	activeCount, pendingCount, err := s.MemoryCounts(MemoryScopeWorkspace, ws.ID)
	if err != nil {
		t.Fatalf("MemoryCounts(workspace): %v", err)
	}
	if activeCount != 1 || pendingCount != 0 {
		t.Fatalf("workspace counts active/pending = %d/%d, want 1/0", activeCount, pendingCount)
	}
	activeCount, pendingCount, err = s.MemoryCounts(MemoryScopeGlobal, "")
	if err != nil {
		t.Fatalf("MemoryCounts(global): %v", err)
	}
	if activeCount != 0 || pendingCount != 1 {
		t.Fatalf("global counts active/pending = %d/%d, want 0/1", activeCount, pendingCount)
	}

	if _, err := s.SetMemoryStatus(pending.ID, MemoryStatusActive); err != nil {
		t.Fatalf("SetMemoryStatus(active): %v", err)
	}
	if activeCount, pendingCount, err = s.MemoryCounts(MemoryScopeGlobal, ""); err != nil {
		t.Fatalf("MemoryCounts(global after accept): %v", err)
	} else if activeCount != 1 || pendingCount != 0 {
		t.Fatalf("global counts after accept = %d/%d, want 1/0", activeCount, pendingCount)
	}

	if err := s.MarkMemoriesUsed([]string{first.ID, pending.ID, "missing-id"}); err != nil {
		t.Fatalf("MarkMemoriesUsed: %v", err)
	}
	used, err := s.GetMemory(first.ID)
	if err != nil {
		t.Fatalf("GetMemory after use: %v", err)
	}
	if used.LastUsedAt.IsZero() {
		t.Fatal("last_used_at was not recorded")
	}

	if err := s.SupersedeMemory(first.ID, pending.ID); err != nil {
		t.Fatalf("SupersedeMemory: %v", err)
	}
	superseded, err := s.GetMemory(first.ID)
	if err != nil {
		t.Fatalf("GetMemory superseded: %v", err)
	}
	if superseded.Status != MemoryStatusSuperseded || superseded.SupersededBy != pending.ID {
		t.Fatalf("superseded memory = %+v, want status superseded by %q", superseded, pending.ID)
	}
	if activeCount, _, err := s.MemoryCounts(MemoryScopeWorkspace, ws.ID); err != nil || activeCount != 0 {
		t.Fatalf("workspace active after supersede = %d (%v), want 0", activeCount, err)
	}
}

// TestMemoryScopesMigrationFromV2 pins the schema-v2 upgrade: an existing
// workspace memory keeps its content and gains workspace/manual/active/project
// defaults, the database reports version 3 and the migration is logged.
func TestMemoryScopesMigrationFromV2(t *testing.T) {
	dir := t.TempDir()
	legacy := openLegacyV1(t, dir)
	if err := ensureMigrationLog(legacy); err != nil {
		t.Fatalf("ensureMigrationLog: %v", err)
	}
	if err := applyMigrationStep(legacy, schemaMigrations[0]); err != nil {
		t.Fatalf("apply v2 step: %v", err)
	}
	now := nowUTCString()
	if _, err := legacy.Exec(
		`INSERT INTO memories(id, workspace_id, content, created_at, updated_at)
		 VALUES ('m-old', 'w1', 'Legacy memory body.', ?, ?)`, now, now,
	); err != nil {
		t.Fatalf("insert legacy memory: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(v2): %v", err)
	}
	defer func() { _ = s.Close() }()

	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion)
	}
	var logged int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = 3`).Scan(&logged); err != nil {
		t.Fatalf("count v3 migration log: %v", err)
	}
	if logged != 1 {
		t.Fatalf("v3 migration log rows = %d, want 1", logged)
	}

	got, err := s.ListMemories("w1", 10)
	if err != nil {
		t.Fatalf("ListMemories after upgrade: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("legacy memories after upgrade = %d, want 1", len(got))
	}
	m := got[0]
	if m.Content != "Legacy memory body." || m.Scope != MemoryScopeWorkspace ||
		m.Kind != MemoryKindProject || m.Source != MemorySourceManual ||
		m.Status != MemoryStatusActive || m.WorkspaceID != "w1" {
		t.Fatalf("upgraded legacy memory = %+v, want body + workspace/project/manual/active defaults", m)
	}
	if m.ContentHash == "" {
		t.Fatal("legacy memory has no content hash after upgrade")
	}
	// The new scope API sees the upgraded row through the workspace filter.
	if global, err := s.ListMemoriesScoped(MemoryScopeGlobal, "", "", 10); err != nil {
		t.Fatalf("ListMemoriesScoped(global): %v", err)
	} else if len(global) != 0 {
		t.Fatalf("global scope returned %d upgraded workspace memories, want 0", len(global))
	}
}

// TestRetireMemory pins the cap-eviction path: a retired note leaves the
// active set, keeps its content as superseded history and has no
// self-referencing successor.
func TestRetireMemory(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "retire", t.TempDir())
	created, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "An old low-value note.",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped: %v", err)
	}
	if err := s.RetireMemory(created.ID); err != nil {
		t.Fatalf("RetireMemory: %v", err)
	}
	got, err := s.GetMemory(created.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got.Status != MemoryStatusSuperseded {
		t.Fatalf("status after retire = %q, want superseded", got.Status)
	}
	if got.SupersededBy != "" {
		t.Fatalf("retired memory superseded_by = %q, want empty", got.SupersededBy)
	}
	if active, pending, err := s.MemoryCounts(MemoryScopeWorkspace, ws.ID); err != nil {
		t.Fatalf("MemoryCounts: %v", err)
	} else if active != 0 || pending != 0 {
		t.Fatalf("counts after retire = %d/%d, want 0/0", active, pending)
	}
	if err := s.RetireMemory(created.ID); err != nil {
		t.Fatalf("second RetireMemory: %v", err)
	}
	if err := s.RetireMemory("missing-id"); err == nil {
		t.Fatal("RetireMemory(unknown id) returned no error")
	}
}

// TestDeleteWorkspaceCascadesWorkspaceMemories pins that removing a workspace
// removes its own memories (memories have no FK to workspaces) while the global
// scope survives.
func TestDeleteWorkspaceCascadesWorkspaceMemories(t *testing.T) {
	s := openTestStore(t)
	doomed, err := s.CreateWorkspace("doomed", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	keep, err := s.CreateWorkspace("keep", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace keep: %v", err)
	}
	if _, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: doomed.ID, Content: "doomed workspace note",
	}); err != nil {
		t.Fatalf("CreateMemoryScoped(doomed): %v", err)
	}
	if _, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: keep.ID, Content: "kept workspace note",
	}); err != nil {
		t.Fatalf("CreateMemoryScoped(keep): %v", err)
	}
	if _, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeGlobal, Content: "global preference",
	}); err != nil {
		t.Fatalf("CreateMemoryScoped(global): %v", err)
	}

	if err := s.DeleteWorkspace(doomed.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	var orphans int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM memories WHERE workspace_id = ?`, doomed.ID,
	).Scan(&orphans); err != nil {
		t.Fatalf("count orphan memories: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("orphan workspace memories after delete = %d, want 0", orphans)
	}
	if rows, err := s.ListMemoriesScoped(MemoryScopeWorkspace, keep.ID, "", 10); err != nil {
		t.Fatalf("ListMemoriesScoped(keep): %v", err)
	} else if len(rows) != 1 {
		t.Fatalf("kept workspace memories = %d, want 1", len(rows))
	}
	if rows, err := s.ListMemoriesScoped(MemoryScopeGlobal, "", "", 10); err != nil {
		t.Fatalf("ListMemoriesScoped(global): %v", err)
	} else if len(rows) != 1 {
		t.Fatalf("global memories after workspace delete = %d, want 1", len(rows))
	}
}

// TestMemoryContentLineEndingsAreCanonical pins that CRLF content is stored as
// LF and hashes identically, so the scoped dedupe matches across line-ending
// variants (the internal/memory.Hash contract).
func TestMemoryContentLineEndingsAreCanonical(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "endings", t.TempDir())

	crlf, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "line one\r\nline two",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped(crlf): %v", err)
	}
	if crlf.Content != "line one\nline two" {
		t.Fatalf("stored content = %q, want LF-normalized", crlf.Content)
	}
	if crlf.ContentHash != hashMemoryContent("line one\nline two") {
		t.Fatalf("content hash is not the canonical LF hash")
	}
	lf, err := s.CreateMemoryScoped(Memory{
		Scope: MemoryScopeWorkspace, WorkspaceID: ws.ID, Content: "line one\nline two",
	})
	if err != nil {
		t.Fatalf("CreateMemoryScoped(lf): %v", err)
	}
	if lf.ID != crlf.ID {
		t.Fatalf("LF duplicate created a new row %q, want the existing %q", lf.ID, crlf.ID)
	}
}
