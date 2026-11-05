package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
)

// newParentTurnForApproval builds a parent turn context with a live event sink
// and returns it plus the sink channel and closer. It mirrors what
// Agent.RunWithAttachments installs at the start of a root turn.
func newParentTurnForApproval(cancelable bool) (context.Context, context.CancelFunc, chan state.Event, *eventSink) {
	ch := make(chan state.Event, 8)
	sink := newEventSink(ch)
	base := context.Background()
	var cancel context.CancelFunc = func() {}
	if cancelable {
		base, cancel = context.WithCancel(base)
	}
	parent := withTurnContext(base, &turnContext{sink: sink, userRequest: "please clean the workspace"})
	return parent, cancel, ch, sink
}

// TestWithTurnApprovalFromKeepsApprovalButNotCancellation pins the RT-05 seam
// contract: the parent's approval channel is visible, while cancellation comes
// from the child, so ending the spawning turn must not cancel an async member.
func TestWithTurnApprovalFromKeepsApprovalButNotCancellation(t *testing.T) {
	parent, cancelParent, _, _ := newParentTurnForApproval(true)
	child := context.Background()
	combined := WithTurnApprovalFrom(parent, child)
	if !HasTurnApproval(combined) {
		t.Fatal("HasTurnApproval(combined) = false, want true")
	}
	if got := turnContextFromContext(combined); got == nil || got.userRequest != "please clean the workspace" {
		t.Fatalf("inherited turn context = %+v, want the parent's user request", got)
	}
	cancelParent()
	if err := combined.Err(); err != nil {
		t.Fatalf("combined.Err() = %v after the parent was cancelled, want nil (child lifetime wins)", err)
	}
	if !HasTurnApproval(combined) {
		t.Fatal("approval channel disappeared when the parent context was cancelled")
	}
}

// TestWithTurnApprovalFromCopiesChildValues pins that the child's own values win
// and the parent's generic values are a fallback (spawn-context passthrough).
func TestWithTurnApprovalFromCopiesChildValues(t *testing.T) {
	type key string
	const parentKey key = "parent"
	const childKey key = "child"
	parent := context.WithValue(context.Background(), parentKey, "from-parent")
	child := context.WithValue(context.Background(), childKey, "from-child")
	parent = withTurnContext(parent, &turnContext{sink: newEventSink(make(chan state.Event, 1))})
	combined := WithTurnApprovalFrom(parent, child)
	if got := combined.Value(childKey); got != "from-child" {
		t.Fatalf("child value = %v, want from-child", got)
	}
	if got := combined.Value(parentKey); got != "from-parent" {
		t.Fatalf("parent value passthrough = %v, want from-parent", got)
	}
}

// TestInheritedApprovalReachesParentTurnAndAllows pins the functional half of
// RT-05: a gated call in a delegated run publishes its ApprovalRequest on the
// parent turn's sink, and an Allow decision lets the call run.
func TestInheritedApprovalReachesParentTurnAndAllows(t *testing.T) {
	parent, cancelParent, events, _ := newParentTurnForApproval(false)
	defer cancelParent()
	gate := NewApprovalQueue(func(string, json.RawMessage) bool { return true })
	ctx := WithTurnApprovalFrom(parent, context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- awaitGateApproval(ctx, gate, "call-inherited", "bash", nil, "review says deny")
	}()

	select {
	case ev := <-events:
		req, ok := ev.(state.ApprovalRequest)
		if !ok {
			t.Fatalf("event = %T, want state.ApprovalRequest", ev)
		}
		if req.CallID != "call-inherited" || req.Name != "bash" {
			t.Fatalf("approval request = %+v, want call-inherited/bash", req)
		}
		if req.ReviewReason != "review says deny" {
			t.Fatalf("review reason = %q, want it carried through", req.ReviewReason)
		}
		if !gate.Approve(req.CallID) {
			t.Fatal("gate.Approve returned false for the published request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval request never reached the parent turn sink")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("awaitGateApproval after Allow = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitGateApproval did not return after Allow")
	}
}

// TestInheritedApprovalDenyFailsTheCall pins that a Deny decision is honoured
// through the inherited channel.
func TestInheritedApprovalDenyFailsTheCall(t *testing.T) {
	parent, cancelParent, events, _ := newParentTurnForApproval(false)
	defer cancelParent()
	gate := NewApprovalQueue(func(string, json.RawMessage) bool { return true })
	ctx := WithTurnApprovalFrom(parent, context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- awaitGateApproval(ctx, gate, "call-denied", "write_file", nil, "")
	}()
	select {
	case ev := <-events:
		req := ev.(state.ApprovalRequest)
		if !gate.Deny(req.CallID) {
			t.Fatal("gate.Deny returned false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval request never reached the parent turn sink")
	}
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "denied") {
			t.Fatalf("awaitGateApproval after Deny = %v, want a denied error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitGateApproval did not return after Deny")
	}
}

// TestInheritedApprovalFailsClosedAfterParentTurnEnds pins the required
// post-turn behaviour: once the parent turn's sink is closed, a gated call in a
// member fails closed instead of hanging or running unapproved.
func TestInheritedApprovalFailsClosedAfterParentTurnEnds(t *testing.T) {
	parent, cancelParent, events, sink := newParentTurnForApproval(false)
	defer cancelParent()
	gate := NewApprovalQueue(func(string, json.RawMessage) bool { return true })
	ctx := WithTurnApprovalFrom(parent, context.Background())
	sink.close() // the parent turn is over
	select {
	case <-events: // drain anything sent before close
	default:
	}
	if HasTurnApproval(ctx) {
		// The sink pointer is still inherited, but it is closed: sending must
		// fail closed. HasTurnApproval documents the channel exists; the send
		// path is what rejects it.
	}
	err := awaitGateApproval(ctx, gate, "call-late", "bash", nil, "")
	if err == nil {
		t.Fatal("awaitGateApproval after the parent turn ended = nil error, want fail-closed")
	}
	if !strings.Contains(err.Error(), "dropped") && !strings.Contains(err.Error(), "approval") {
		t.Fatalf("error = %q, want an approval-channel failure", err)
	}
}

// TestWithTurnApprovalFromWithoutParentApproval pins that a plain context
// contributes no approval channel, so the delegated-run guidance stays active.
func TestWithTurnApprovalFromWithoutParentApproval(t *testing.T) {
	if HasTurnApproval(context.Background()) {
		t.Fatal("HasTurnApproval(background) = true, want false")
	}
	combined := WithTurnApprovalFrom(context.Background(), context.Background())
	if HasTurnApproval(combined) {
		t.Fatal("HasTurnApproval(plain parent, plain child) = true, want false")
	}
	err := awaitGateApproval(WithDelegatedApproval(combined), &staticGate{require: true}, "c", "bash", nil, "")
	if err == nil || !strings.Contains(err.Error(), "no approval channel") {
		t.Fatalf("delegated gated call = %v, want the fail-closed guidance", err)
	}
}
