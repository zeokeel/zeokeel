package uiroot

// Read-model tests for the host-side activity status (P2-9): the window title
// and the system tray show how many conversations run and how many approvals
// wait, so the two surfaces need one live count that cannot disagree with the
// close guard's view of the backend.

import (
	"context"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// activityChat is a ChatService with a scripted conversation list.
type activityChat struct {
	sessions []contracts.ChatSessionInfo
}

func (c *activityChat) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (c *activityChat) Sessions() []contracts.ChatSessionInfo { return c.sessions }
func (c *activityChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (c *activityChat) ActiveSession() string         { return "" }
func (c *activityChat) SetActiveSession(string) error { return nil }
func (c *activityChat) NewSession(string) (string, error) {
	return "", nil
}
func (c *activityChat) DeleteSession(string) error { return nil }
func (c *activityChat) Send(string, string) error  { return nil }
func (c *activityChat) Cancel(string) error        { return nil }
func (c *activityChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

// activityApprovals is a minimal contracts.Approvals with a scripted pending
// set.
type activityApprovals struct {
	pending []contracts.ApprovalRequest
}

func (a *activityApprovals) Mode() string { return "ask" }
func (a *activityApprovals) Request(context.Context, contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalAllow, nil
}
func (a *activityApprovals) Pending() []contracts.ApprovalRequest {
	return append([]contracts.ApprovalRequest(nil), a.pending...)
}
func (a *activityApprovals) Resolve(id string, _ contracts.ApprovalDecision) error {
	kept := a.pending[:0]
	for _, request := range a.pending {
		if request.ID != id {
			kept = append(kept, request)
		}
	}
	a.pending = kept
	return nil
}
func (a *activityApprovals) Subscribe(func()) (func(), error) { return func() {}, nil }

// TestActivityCountsRunsAndPendingApprovals covers the counters: only running
// conversations count, pending approvals count one each, and a host without the
// services reports no activity instead of failing.
func TestActivityCountsRunsAndPendingApprovals(t *testing.T) {
	chat := &activityChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Running: true},
		{ID: "s2", Running: true},
		{ID: "s3"},
		{ID: "s4", Unread: true},
	}}
	approvals := &activityApprovals{pending: []contracts.ApprovalRequest{
		{ID: "a1", SessionID: "s1"},
		{ID: "a2", SessionID: "s2"},
	}}
	root := New(Config{AppName: "ActivityTest", Chat: chat})
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	if _, err := kernel.Provide(contracts.KeyApprovals, contracts.Approvals(approvals)); err != nil {
		t.Fatalf("provide approvals: %v", err)
	}
	root.SetKernelContext(kernel)

	status := root.Activity()
	if status.Running != 2 {
		t.Fatalf("Activity().Running = %d, want 2", status.Running)
	}
	if status.Approvals != 2 {
		t.Fatalf("Activity().Approvals = %d, want 2", status.Approvals)
	}

	// A resolved approval drops out of the count; the same call the close guard
	// makes (RunningWork) sees the remaining run.
	if err := approvals.Resolve("a1", contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := root.Activity().Approvals; got != 1 {
		t.Fatalf("Activity().Approvals after resolve = %d, want 1", got)
	}
	if !root.RunningWork() {
		t.Fatal("RunningWork() = false with a running conversation")
	}

	// A stripped host reports no activity at all instead of panicking.
	empty := New(Config{AppName: "ActivityTest"})
	if zero := empty.Activity(); zero.Running != 0 || zero.Approvals != 0 {
		t.Fatalf("Activity() on a bare root = %+v, want zero", zero)
	}
	var nilRoot *Root
	if zero := nilRoot.Activity(); zero.Running != 0 || zero.Approvals != 0 {
		t.Fatalf("Activity() on a nil root = %+v, want zero", zero)
	}
}
