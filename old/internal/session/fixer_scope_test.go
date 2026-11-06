package session_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
)

// TestOpenSessionAppliesPersistedAgentMode is the regression for the mode
// hydration gap: reopening a Deep session reported Deep from the controller
// but left the shared runtime agent in Standard.
func TestOpenSessionAppliesPersistedAgentMode(t *testing.T) {
	h := newModeHarness(t, &fakeProvider{}, nil)
	h.ctrl.SetAgentModeSwitcher(h.agent.SetMode)
	ctx := context.Background()
	deepID := h.sessionID
	if err := h.ctrl.SetMode(runtime.ModeDeep); err != nil {
		t.Fatalf("SetMode(deep): %v", err)
	}
	if got := h.agent.Mode(); got != runtime.ModeDeep {
		t.Fatalf("agent mode after SetMode = %q, want deep", got)
	}

	// A new session without a preset starts Standard and resets the shared
	// agent instead of inheriting the previous session's Deep topology.
	otherID, err := h.ctrl.CreateSession(ctx, "other")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := h.agent.Mode(); got != runtime.ModeStandard {
		t.Fatalf("agent mode after new session = %q, want standard", got)
	}
	if _, err := h.ctrl.OpenSession(ctx, otherID); err != nil {
		t.Fatalf("OpenSession(other): %v", err)
	}

	// Reopening the Deep session must switch the shared agent back.
	if _, err := h.ctrl.OpenSession(ctx, deepID); err != nil {
		t.Fatalf("OpenSession(deep): %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeDeep {
		t.Fatalf("controller Mode = %q, want deep", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeDeep {
		t.Fatalf("agent Mode after reopening deep session = %q, want deep", got)
	}
}

// TestOpenSessionSelectsTheSessionsWorkspace covers the cross-workspace jump:
// opening a session from another workspace left the agent's root, Files tab
// and session_search scope on the old workspace.
func TestOpenSessionSelectsTheSessionsWorkspace(t *testing.T) {
	h := newModeHarness(t, &fakeProvider{}, nil)
	ctx := context.Background()
	firstID := h.ctrl.CurrentWorkspaceID()

	second, err := h.ctrl.CreateWorkspace(ctx, "second", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	otherSession, err := h.ctrl.CreateSessionInWorkspace(ctx, second.ID, "second session")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	if err := h.ctrl.SelectWorkspace(ctx, firstID); err != nil {
		t.Fatalf("SelectWorkspace(first): %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != firstID {
		t.Fatalf("current workspace = %q, want first %q", got, firstID)
	}

	if _, err := h.ctrl.OpenSession(ctx, otherSession); err != nil {
		t.Fatalf("OpenSession(cross workspace): %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != second.ID {
		t.Fatalf("workspace after opening cross-workspace session = %q, want %q", got, second.ID)
	}
	if got := h.ctrl.WorkspaceRoot(); got != second.Root {
		t.Fatalf("WorkspaceRoot = %q, want %q", got, second.Root)
	}
	if got := h.ctrl.CurrentSessionID(); got != otherSession {
		t.Fatalf("CurrentSessionID = %q, want %q", got, otherSession)
	}
}

// TestDeleteSessionClearsEveryPerSessionSetting pins the dangling
// session.<id>.model row DeleteSession used to leave behind.
func TestDeleteSessionClearsEveryPerSessionSetting(t *testing.T) {
	h := newModeHarness(t, &fakeProvider{}, nil)
	ctx := context.Background()
	id := h.sessionID
	if err := h.store.SetSetting(session.SessionModelKey(id), "model-x"); err != nil {
		t.Fatalf("seed model key: %v", err)
	}
	if err := h.store.SetSetting(session.SessionPermissionKey(id), session.PermissionFull); err != nil {
		t.Fatalf("seed permission key: %v", err)
	}
	if err := h.store.SetSetting(session.SessionModeKey(id), runtime.ModeDeep); err != nil {
		t.Fatalf("seed mode key: %v", err)
	}
	if err := h.ctrl.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	for _, key := range []string{
		session.SessionPermissionKey(id),
		session.SessionModeKey(id),
		session.SessionModelKey(id),
	} {
		raw, err := h.store.GetSetting(key)
		if err != nil && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("GetSetting(%s): %v", key, err)
		}
		if strings.TrimSpace(raw) != "" {
			t.Errorf("%s = %q after DeleteSession, want cleared", key, raw)
		}
	}
}

// TestDeleteWorkspaceClearsEveryPerSessionSetting covers the same contract for
// a workspace cascade.
func TestDeleteWorkspaceClearsEveryPerSessionSetting(t *testing.T) {
	h := newModeHarness(t, &fakeProvider{}, nil)
	ctx := context.Background()
	ws, err := h.ctrl.CreateWorkspace(ctx, "doomed", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	id, err := h.ctrl.CreateSessionInWorkspace(ctx, ws.ID, "doomed session")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	for key, value := range map[string]string{
		session.SessionModelKey(id):      "model-x",
		session.SessionPermissionKey(id): session.PermissionFull,
		session.SessionModeKey(id):       runtime.ModeDeep,
	} {
		if err := h.store.SetSetting(key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	if err := h.ctrl.DeleteWorkspace(ctx, ws.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	for _, key := range []string{
		session.SessionPermissionKey(id),
		session.SessionModeKey(id),
		session.SessionModelKey(id),
	} {
		raw, err := h.store.GetSetting(key)
		if err != nil && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("GetSetting(%s): %v", key, err)
		}
		if strings.TrimSpace(raw) != "" {
			t.Errorf("%s = %q after DeleteWorkspace, want cleared", key, raw)
		}
	}
}
