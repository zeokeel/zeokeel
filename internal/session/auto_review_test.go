package session_test

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
)

// TestAutoReviewPermissionNormalizesAndPersists covers the whole session-level
// round trip of the experimental permission: normalization accepts it, the
// settings store loads it back, a new session inherits it, SetSessionPermission
// persists it per session, a permission-aware rebuild fires, and a restart
// rehydrates it instead of falling back to the global default.
func TestAutoReviewPermissionNormalizesAndPersists(t *testing.T) {
	if got := session.NormalizeSettings(session.Settings{Permission: session.PermissionAutoReview}).Permission; got != session.PermissionAutoReview {
		t.Fatalf("NormalizeSettings(auto_review) = %q, want %q", got, session.PermissionAutoReview)
	}
	if got := session.NormalizeSettings(session.Settings{Permission: "unknown"}).Permission; got != session.PermissionWorkspace {
		t.Fatalf("NormalizeSettings(unknown) = %q, want the safe default %q", got, session.PermissionWorkspace)
	}

	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Permission = session.PermissionAutoReview
	if err := h.ctrl.Settings().Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if loaded.Permission != session.PermissionAutoReview {
		t.Fatalf("persisted global permission = %q, want %q", loaded.Permission, session.PermissionAutoReview)
	}

	id, err := h.ctrl.CreateSession(ctx, "auto review session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := h.ctrl.SessionPermission(id); got != session.PermissionAutoReview {
		t.Fatalf("new session permission = %q, want %q", got, session.PermissionAutoReview)
	}
	raw, err := h.store.GetSetting(session.SessionPermissionKey(id))
	if err != nil || raw != session.PermissionAutoReview {
		t.Fatalf("stored %s = %q, %v; want %q",
			session.SessionPermissionKey(id), raw, err, session.PermissionAutoReview)
	}

	// Switching the current session to auto review must ask the app to
	// rebuild: the reviewer and approval patterns are build-time state.
	var mu sync.Mutex
	var reasons []string
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		mu.Lock()
		reasons = append(reasons, reason)
		mu.Unlock()
		return nil
	})
	h.ctrl.SetBuiltPermission(session.PermissionWorkspace)
	h.ctrl.SetSessionPermission(id, session.PermissionAutoReview)
	mu.Lock()
	gotReasons := append([]string(nil), reasons...)
	mu.Unlock()
	if len(gotReasons) != 1 || gotReasons[0] != "permission" {
		t.Fatalf("rebuild reasons = %v, want [permission]", gotReasons)
	}

	// Invalid values are still rejected and leave the stored mode intact.
	h.ctrl.SetSessionPermission(id, "bogus")
	if got := h.ctrl.SessionPermissionOrDefault(id); got != session.PermissionAutoReview {
		t.Fatalf("permission after rejected value = %q, want %q", got, session.PermissionAutoReview)
	}

	// Restart over the same store: the per-session mode must survive instead
	// of inheriting the controller's global default.
	cfg, err = h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load before restart: %v", err)
	}
	cfg.Permission = session.PermissionAsk
	if err := h.ctrl.Settings().Save(cfg); err != nil {
		t.Fatalf("Save global default before restart: %v", err)
	}
	ctrl := h.restart()
	if _, err := ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if got := ctrl.SessionPermissionOrDefault(id); got != session.PermissionAutoReview {
		t.Fatalf("after restart permission = %q, want %q (global default is now Read Only)", got, session.PermissionAutoReview)
	}
}
