package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMemoryCRUDAndPersistence covers the durable memories table: create,
// list, get, update, delete and clear all round-trip, and a row survives
// reopening the index.
func TestMemoryCRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, err := s.CreateWorkspace("memory ws", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	created, err := s.CreateMemory(ws.ID, "  The release branch is release/2026.  ")
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateMemory generated no id")
	}
	if created.WorkspaceID != ws.ID {
		t.Fatalf("WorkspaceID = %q, want %q", created.WorkspaceID, ws.ID)
	}
	if created.Content != "The release branch is release/2026." {
		t.Fatalf("Content = %q, want trimmed", created.Content)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", created)
	}

	got, err := s.GetMemory(created.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got.ID != created.ID || got.Content != created.Content {
		t.Fatalf("GetMemory = %+v, want %+v", got, created)
	}

	// The row is durable, not cached in memory.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	list, err := s.ListMemories(ws.ID, 0)
	if err != nil {
		t.Fatalf("ListMemories after reopen: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListMemories after reopen = %+v, want the created memory", list)
	}
	if !list[0].CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", list[0].CreatedAt, created.CreatedAt)
	}

	// Update moves UpdatedAt and keeps the workspace and CreatedAt.
	time.Sleep(time.Millisecond)
	updated, err := s.UpdateMemory(created.ID, "The release branch is release/2027.")
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if updated.Content != "The release branch is release/2027." {
		t.Fatalf("updated Content = %q", updated.Content)
	}
	if updated.WorkspaceID != ws.ID {
		t.Fatalf("updated WorkspaceID = %q, want %q", updated.WorkspaceID, ws.ID)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("UpdateMemory changed CreatedAt: %v -> %v", created.CreatedAt, updated.CreatedAt)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want after %v", updated.UpdatedAt, created.UpdatedAt)
	}

	if err := s.DeleteMemory(created.ID); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	if _, err := s.GetMemory(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMemory after delete error = %v, want ErrNotFound", err)
	}
	if err := s.DeleteMemory(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteMemory error = %v, want ErrNotFound", err)
	}

	// Clear removes every row in the workspace and reports the count.
	for i := 0; i < 3; i++ {
		if _, err := s.CreateMemory(ws.ID, "note"); err != nil {
			t.Fatalf("CreateMemory #%d: %v", i, err)
		}
	}
	cleared, err := s.ClearMemories(ws.ID)
	if err != nil {
		t.Fatalf("ClearMemories: %v", err)
	}
	if cleared != 3 {
		t.Fatalf("ClearMemories = %d, want 3", cleared)
	}
	list, err = s.ListMemories(ws.ID, 0)
	if err != nil {
		t.Fatalf("ListMemories after clear: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListMemories after clear = %+v, want empty", list)
	}
}

// TestMemoryWorkspaceScopeIsolation pins that every read and clear is scoped
// to one workspace, and that a lookup outside the scope can be rejected from
// the row's workspace id.
func TestMemoryWorkspaceScopeIsolation(t *testing.T) {
	s := openTestStore(t)
	alpha, err := s.CreateWorkspace("alpha", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace alpha: %v", err)
	}
	beta, err := s.CreateWorkspace("beta", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace beta: %v", err)
	}
	a, err := s.CreateMemory(alpha.ID, "alpha only: run make lint")
	if err != nil {
		t.Fatalf("CreateMemory alpha: %v", err)
	}
	b, err := s.CreateMemory(beta.ID, "beta only: run make test")
	if err != nil {
		t.Fatalf("CreateMemory beta: %v", err)
	}

	list, err := s.ListMemories(alpha.ID, 0)
	if err != nil {
		t.Fatalf("ListMemories alpha: %v", err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("ListMemories alpha = %+v, want only %q", list, a.ID)
	}
	list, err = s.ListMemories(beta.ID, 0)
	if err != nil {
		t.Fatalf("ListMemories beta: %v", err)
	}
	if len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("ListMemories beta = %+v, want only %q", list, b.ID)
	}

	// A search is scoped too: alpha never sees beta's content.
	list, err = s.SearchMemories(alpha.ID, "make test")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("SearchMemories alpha found beta rows: %+v", list)
	}

	// A row fetched by id still carries its workspace so the tool layer can
	// reject an update/delete from another workspace.
	got, err := s.GetMemory(b.ID)
	if err != nil {
		t.Fatalf("GetMemory beta: %v", err)
	}
	if got.WorkspaceID != beta.ID {
		t.Fatalf("GetMemory workspace = %q, want %q", got.WorkspaceID, beta.ID)
	}

	cleared, err := s.ClearMemories(alpha.ID)
	if err != nil {
		t.Fatalf("ClearMemories alpha: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("ClearMemories alpha = %d, want 1", cleared)
	}
	if _, err := s.GetMemory(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alpha memory survived clear: %v", err)
	}
	if _, err := s.GetMemory(b.ID); err != nil {
		t.Fatalf("ClearMemories removed beta's memory: %v", err)
	}
}

// TestMemoryValidationAndLimits covers the content cap, empty input and page
// size clamping.
func TestMemoryValidationAndLimits(t *testing.T) {
	s := openTestStore(t)
	ws, err := s.CreateWorkspace("limits", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if _, err := s.CreateMemory(ws.ID, "   "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateMemory(blank) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.CreateMemory("", "note"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateMemory(no workspace) error = %v, want ErrInvalidArgument", err)
	}
	tooLarge := strings.Repeat("x", MaxMemoryContentBytes+1)
	if _, err := s.CreateMemory(ws.ID, tooLarge); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateMemory(>8KB) error = %v, want ErrInvalidArgument", err)
	}
	exact := strings.Repeat("x", MaxMemoryContentBytes)
	if _, err := s.CreateMemory(ws.ID, exact); err != nil {
		t.Fatalf("CreateMemory(8KB) error = %v, want success", err)
	}
	if _, err := s.UpdateMemory("", "note"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdateMemory(no id) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.UpdateMemory("missing", tooLarge); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdateMemory(oversized) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.SearchMemories(ws.ID, "  "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SearchMemories(blank) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.ListMemories("", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ListMemories(no workspace) error = %v, want ErrInvalidArgument", err)
	}

	// Fill past the default page size; a plain list is capped at 50 and an
	// explicit larger limit is clamped to 200.
	for i := 0; i < 60; i++ {
		if _, err := s.CreateMemory(ws.ID, "note "+strings.Repeat("x", i%10)); err != nil {
			t.Fatalf("CreateMemory fill #%d: %v", i, err)
		}
	}
	list, err := s.ListMemories(ws.ID, 0)
	if err != nil {
		t.Fatalf("ListMemories(default): %v", err)
	}
	if len(list) != DefaultMemoryLimit {
		t.Fatalf("ListMemories(default) = %d rows, want %d", len(list), DefaultMemoryLimit)
	}
	list, err = s.ListMemories(ws.ID, 1000)
	if err != nil {
		t.Fatalf("ListMemories(1000): %v", err)
	}
	// 60 filled notes plus the exact-8KB note created above.
	if len(list) != 61 {
		t.Fatalf("ListMemories(1000) = %d rows, want 61", len(list))
	}
	list, err = s.ListMemories(ws.ID, 5)
	if err != nil {
		t.Fatalf("ListMemories(5): %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("ListMemories(5) = %d rows, want 5", len(list))
	}
}

// TestMemorySearchLiteralMetacharacters proves the LIKE escape: "%" and "_"
// are matched literally instead of acting as wildcards.
func TestMemorySearchLiteralMetacharacters(t *testing.T) {
	s := openTestStore(t)
	ws, err := s.CreateWorkspace("search", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	for _, content := range []string{
		"coverage is 100% now",
		"the 100 files were counted",
		"snake_case_name is reserved",
		"snakeXcaseXname is not",
	} {
		if _, err := s.CreateMemory(ws.ID, content); err != nil {
			t.Fatalf("CreateMemory(%q): %v", content, err)
		}
	}

	got, err := s.SearchMemories(ws.ID, "100%")
	if err != nil {
		t.Fatalf("SearchMemories(100%%): %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Content, "100%") {
		t.Fatalf("SearchMemories(100%%) = %+v, want only the literal match", got)
	}
	got, err = s.SearchMemories(ws.ID, "snake_case")
	if err != nil {
		t.Fatalf("SearchMemories(snake_case): %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Content, "snake_case") {
		t.Fatalf("SearchMemories(snake_case) = %+v, want only the literal match", got)
	}
}
