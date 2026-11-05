package session_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
)

// sessionPermissionRebuilds wires a counting rebuild hook and returns the
// recorded reasons and the current session id the app would build for.
type permissionRebuilds struct {
	mu       sync.Mutex
	reasons  []string
	sessions []string
	err      error
}

func (r *permissionRebuilds) hook(ctrl *session.Controller) func(string) error {
	return func(reason string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.reasons = append(r.reasons, reason)
		r.sessions = append(r.sessions, ctrl.CurrentSessionID())
		return r.err
	}
}

func (r *permissionRebuilds) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reasons...), append([]string(nil), r.sessions...)
}

// TestSubmitRebuildsForTargetSessionPermission is the RT-02 regression: the
// automation path (CreateSession + Submit) must not inherit the previous
// session's build-time access mode. A Full session followed by a default
// Workspace session must rebuild before the background turn runs, and the app
// must see the target session as current while rebuilding.
func TestSubmitRebuildsForTargetSessionPermission(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	first, err := h.ctrl.CreateSession(ctx, "user full session")
	if err != nil {
		t.Fatal(err)
	}
	// The user switched the first session to Full access and the app rebuilt
	// the shared agent, recording the build-time mode.
	h.ctrl.SetSessionPermission(first, session.PermissionFull)
	h.ctrl.SetBuiltPermission(session.PermissionFull)

	// The automation scheduler creates a new session (default Workspace) and
	// submits its prompt.
	second, err := h.ctrl.CreateSession(ctx, "automation task")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.ctrl.SessionPermissionOrDefault(second); got != session.PermissionWorkspace {
		t.Fatalf("new session permission = %q, want %q", got, session.PermissionWorkspace)
	}

	var rebuilds permissionRebuilds
	h.ctrl.SetAgentRebuilder(rebuilds.hook(h.ctrl))
	if err := h.ctrl.Submit(ctx, second, "run scheduled task"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	reasons, builtFor := rebuilds.snapshot()
	if len(reasons) != 1 || reasons[0] != "permission" {
		t.Fatalf("rebuild reasons = %v, want [permission] before the background turn", reasons)
	}
	if len(builtFor) != 1 || builtFor[0] != second {
		t.Fatalf("agent rebuilt for current session %v, want target session %q", builtFor, second)
	}

	// A turn whose session already matches the built mode must not rebuild.
	h.ctrl.SetBuiltPermission(session.PermissionWorkspace)
	if err := h.ctrl.Submit(ctx, second, "second background turn"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	if reasons, _ := rebuilds.snapshot(); len(reasons) != 1 {
		t.Fatalf("matching permission rebuilt again: %v", reasons)
	}
}

// TestSubmitRefusesTurnWhenPermissionRebuildFails covers the security side of
// RT-02: if the app cannot rebuild the tool set for the target session's
// access mode, Submit must abort instead of running the turn under the old
// (possibly wider) permission.
func TestSubmitRefusesTurnWhenPermissionRebuildFails(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()

	first, err := h.ctrl.CreateSession(ctx, "user full session")
	if err != nil {
		t.Fatal(err)
	}
	h.ctrl.SetSessionPermission(first, session.PermissionFull)
	h.ctrl.SetBuiltPermission(session.PermissionFull)
	second, err := h.ctrl.CreateSession(ctx, "automation task")
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("rebuild failed")
	h.ctrl.SetAgentRebuilder(func(string) error { return boom })
	err = h.ctrl.Submit(ctx, second, "run scheduled task")
	if !errors.Is(err, boom) {
		t.Fatalf("Submit error = %v, want the rebuild failure wrapped", err)
	}
	if got := provider.requestCount(); got != 0 {
		t.Fatalf("provider received %d request(s) after a failed permission rebuild; the turn must not run", got)
	}
}
