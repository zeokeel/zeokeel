package importer

// BE-04 (2026-11 audit): Import used to silently switch the user's current
// workspace — workspace.Create makes the registered root current, so after an
// import the conversation's tool root (unbound sessions follow Current) jumped
// to the last imported cwd without consent. Import now saves and restores the
// current workspace around the registration loop.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// loggerContains reports whether the fake logger recorded a line containing
// fragment (the importer's fakeLogger has no contains helper).
func loggerContains(l *fakeLogger, fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

func TestImportPreservesCurrentWorkspace(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	writeSession(t, home, "wsA", "sA", goodSessionLines(t, "sA", cwdA)...)
	writeSession(t, home, "wsB", "sB", goodSessionLines(t, "sB", cwdB)...)

	store := newFakeStore()
	workspace := newFakeWorkspace()
	// The user's own project is registered and current before the import.
	userProject := t.TempDir()
	userWorkspace, err := workspace.Create(ctx, "user-project", userProject)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	svc := newTestService(t, Config{}, store, workspace)
	result, err := svc.Import(ctx, home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 2 || result.ImportedWorkspaces != 2 {
		t.Fatalf("Import = %+v, want 2 sessions / 2 workspaces", result)
	}
	current, ok, err := workspace.Current(ctx)
	if err != nil || !ok {
		t.Fatalf("Current after import: %v %v", ok, err)
	}
	if current.ID != userWorkspace.ID || current.Root != userProject {
		t.Fatalf("BE-04: Import silently switched current workspace to %+v, want the user's project %+v",
			current, userWorkspace)
	}
	// Both imported roots are still registered (the restore must not unregister).
	registered, _ := workspace.List(ctx)
	if len(registered) != 3 {
		t.Fatalf("registered = %+v, want the user project + 2 imported roots", registered)
	}
}

// With no current workspace before the import there is no user project to
// protect: the first imported root becomes current (documented behavior — the
// workspace contract has no "register without switching" API).
func TestImportWithoutCurrentKeepsRegisteredRoot(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	cwd := t.TempDir()
	writeSession(t, home, "ws", "s1", goodSessionLines(t, "s1", cwd)...)
	workspace := newFakeWorkspace()
	svc := newTestService(t, Config{}, newFakeStore(), workspace)
	if _, err := svc.Import(ctx, home); err != nil {
		t.Fatalf("Import: %v", err)
	}
	current, ok, _ := workspace.Current(ctx)
	if !ok || current.Root != cwd {
		t.Fatalf("current = %+v %v, want the imported root %q", current, ok, cwd)
	}
}

// A failed restore is logged, never fatal: the import itself succeeded.
func TestImportRestoreFailureLoggedNotFatal(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	cwd := t.TempDir()
	writeSession(t, home, "ws", "s1", goodSessionLines(t, "s1", cwd)...)
	workspace := newFakeWorkspace()
	userProject := t.TempDir()
	if _, err := workspace.Create(ctx, "user-project", userProject); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	workspace.openErr = errors.New("disk full")
	logger := &fakeLogger{}
	svc := newService(Config{}, newFakeStore(), workspace, logger)
	result, err := svc.Import(ctx, home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 {
		t.Fatalf("Import = %+v, want the session imported despite the restore failure", result)
	}
	if !loggerContains(logger, "current workspace") {
		t.Fatalf("restore failure not logged: %v", logger.lines)
	}
}
