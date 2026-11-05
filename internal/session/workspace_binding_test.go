package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// cleanRoot normalizes a workspace root for comparisons (t.TempDir paths can
// carry a trailing slash or a clean difference).
func cleanRoot(p string) string { return filepath.Clean(strings.TrimSpace(p)) }

// TestCreateAndSubmitBindSessionWorkspace proves a session's workspace is
// selected before it becomes current, and a later Submit moves the shared agent
// back to that workspace even after the user switched away: this is the
// CreateSessionInWorkspace / queued-send / background-turn path that used to
// run on the wrong workspace root.
func TestCreateAndSubmitBindSessionWorkspace(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	wsA := h.workspace
	rootB := t.TempDir()
	wsB, err := h.store.CreateWorkspace("B", rootB)
	if err != nil {
		t.Fatalf("CreateWorkspace(B): %v", err)
	}

	var (
		mu      sync.Mutex
		reasons []string
	)
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		mu.Lock()
		reasons = append(reasons, reason)
		mu.Unlock()
		return nil
	})
	if err := h.ctrl.SelectWorkspace(ctx, wsA); err != nil {
		t.Fatalf("SelectWorkspace(A): %v", err)
	}

	// Create a session for B while A is selected: the creation itself must
	// select B and rebuild the agent for B's root.
	idB, err := h.ctrl.CreateSessionInWorkspace(ctx, wsB.ID, "B session")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace(B): %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != wsB.ID {
		t.Fatalf("selected workspace = %q, want B (%q)", got, wsB.ID)
	}
	if got, want := cleanRoot(h.ctrl.WorkspaceRoot()), cleanRoot(rootB); got != want {
		t.Fatalf("WorkspaceRoot = %q, want B root %q", got, want)
	}
	mu.Lock()
	sawRebuild := len(reasons) > 0
	mu.Unlock()
	if !sawRebuild {
		t.Fatal("creating a session in B did not rebuild the agent for B's root")
	}

	// The user switches back to A; submitting the B session (queued send,
	// goal continuation, automation) must re-bind B before the turn runs.
	if err := h.ctrl.SelectWorkspace(ctx, wsA); err != nil {
		t.Fatalf("SelectWorkspace(A again): %v", err)
	}
	if err := h.ctrl.Submit(ctx, idB, "work in B"); err != nil {
		t.Fatalf("Submit(B session): %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	if got := h.ctrl.CurrentWorkspaceID(); got != wsB.ID {
		t.Fatalf("selected workspace after Submit(B) = %q, want B", got)
	}
	if got, want := cleanRoot(h.ctrl.WorkspaceRoot()), cleanRoot(rootB); got != want {
		t.Fatalf("WorkspaceRoot after Submit(B) = %q, want B root %q", got, want)
	}
	if got := provider.requestCount(); got != 1 {
		t.Fatalf("provider requests = %d, want 1 (the B turn ran)", got)
	}

	// A plain CreateSession keeps using the now-selected B workspace instead of
	// silently jumping back to A.
	idB2, err := h.ctrl.CreateSession(ctx, "second B session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sessB2, err := h.store.GetSession(idB2)
	if err != nil {
		t.Fatalf("GetSession(B2): %v", err)
	}
	if sessB2.WorkspaceID != wsB.ID {
		t.Fatalf("new session workspace = %q, want B", sessB2.WorkspaceID)
	}
}

// TestWorkspaceRebuildFailureRefusesSessionRowsAndTurns proves the binding is
// fail-closed: when the app cannot rebuild the agent for the session's
// workspace, creating a session there or submitting one refuses instead of
// starting a turn against the old root, and the selection rolls back.
func TestWorkspaceRebuildFailureRefusesSessionRowsAndTurns(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	wsA := h.workspace
	rootB := t.TempDir()
	wsB, err := h.store.CreateWorkspace("B", rootB)
	if err != nil {
		t.Fatalf("CreateWorkspace(B): %v", err)
	}

	// Create the B session while rebuilds still succeed, then switch to A.
	idB, err := h.ctrl.CreateSessionInWorkspace(ctx, wsB.ID, "B session")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace(B): %v", err)
	}
	if err := h.ctrl.SelectWorkspace(ctx, wsA); err != nil {
		t.Fatalf("SelectWorkspace(A): %v", err)
	}
	before, err := h.store.QuerySessions(store.SessionQuery{WorkspaceID: wsB.ID, IncludeArchived: true})
	if err != nil {
		t.Fatalf("QuerySessions(B): %v", err)
	}

	rebuildErr := errors.New("provider unavailable")
	h.ctrl.SetAgentRebuilder(func(string) error { return rebuildErr })

	// Submitting the B session must refuse to run and leave the selection on A
	// (the agent is still built for A's root).
	if err := h.ctrl.Submit(ctx, idB, "work in B"); err == nil ||
		!strings.Contains(err.Error(), "rebuild agent") {
		t.Fatalf("Submit with failing rebuild error = %v, want the rebuild error", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != wsA {
		t.Fatalf("workspace after failed bind = %q, want rollback to A", got)
	}
	if got := provider.requestCount(); got != 0 {
		t.Fatalf("provider requests = %d, want 0 (turn must not start)", got)
	}
	if snap := h.ctrl.Snapshot(); snap == nil || snap.Phase != "idle" {
		t.Fatalf("session phase after refused turn = %+v, want idle", h.ctrl.Snapshot())
	}

	// Creating another session in B is refused too, and writes no row.
	if _, err := h.ctrl.CreateSessionInWorkspace(ctx, wsB.ID, "refused B session"); err == nil ||
		!strings.Contains(err.Error(), "rebuild agent") {
		t.Fatalf("CreateSessionInWorkspace with failing rebuild error = %v, want the rebuild error", err)
	}
	after, err := h.store.QuerySessions(store.SessionQuery{WorkspaceID: wsB.ID, IncludeArchived: true})
	if err != nil {
		t.Fatalf("QuerySessions(B) after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("refused create wrote a session row: before=%d after=%d", len(before), len(after))
	}
}
