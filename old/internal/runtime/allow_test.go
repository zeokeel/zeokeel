package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

func TestApprovalQueueAllowAlways(t *testing.T) {
	q := NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "bash" || toolName == "write_file"
	})
	if !q.RequireApproval("bash", nil) {
		t.Fatal("bash must require approval before AllowAlways")
	}
	if q.RequireApproval("clock", nil) {
		t.Fatal("clock must not require approval")
	}

	// Per-tool: only the allow-listed tool skips approval.
	q.AllowAlways("bash")
	if q.RequireApproval("bash", nil) {
		t.Fatal("bash must not require approval after AllowAlways")
	}
	if !q.RequireApproval("write_file", nil) {
		t.Fatal("write_file must still require approval")
	}
	// An allow-listed tool that the predicate never required is still allowed.
	q.AllowAlways("clock")
	if q.RequireApproval("clock", nil) {
		t.Fatal("clock must not require approval after AllowAlways")
	}

	// ClearRules restores the predicate's behaviour without changing the
	// predicate itself.
	q.ClearRules()
	if !q.RequireApproval("bash", nil) {
		t.Fatal("bash must require approval after ClearRules")
	}
	if q.RequireApproval("clock", nil) {
		t.Fatal("clock predicate result must survive ClearRules")
	}

	// Nil receiver is a no-op, matching the rest of ApprovalQueue.
	var nilQueue *ApprovalQueue
	nilQueue.AllowAlways("bash")
	nilQueue.ClearRules()
	if nilQueue.RequireApproval("bash", nil) {
		t.Fatal("nil queue must not require approval")
	}
}

func TestApprovalQueueAllowAlwaysDoesNotResolvePending(t *testing.T) {
	q := NewApprovalQueue(func(string, json.RawMessage) bool { return true })

	result := make(chan bool, 1)
	go func() {
		allow, err := q.Wait(context.Background(), "call-pending")
		if err != nil {
			t.Errorf("Wait: %v", err)
			result <- true
			return
		}
		result <- allow
	}()
	waitForWaiter(t, q, "call-pending")

	// AllowAlways is about future calls; the in-flight call still waits for the
	// user's explicit decision.
	q.AllowAlways("clock")
	if !q.Deny("call-pending") {
		t.Fatal("Deny did not reach the pending Wait")
	}
	select {
	case allow := <-result:
		if allow {
			t.Fatal("Deny produced an allow")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending Wait did not return after Deny")
	}
	if q.RequireApproval("clock", nil) {
		t.Fatal("clock must stay allow-listed for future calls")
	}
}

func TestApprovalQueueAllowAlwaysConcurrent(t *testing.T) {
	q := NewApprovalQueue(func(string, json.RawMessage) bool { return true })

	const n = 64
	var wg sync.WaitGroup
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			q.AllowAlways("bash")
		}()
		go func() {
			defer wg.Done()
			_ = q.RequireApproval("bash", nil)
		}()
	}
	wg.Wait()

	if q.RequireApproval("bash", nil) {
		t.Fatal("bash must remain allow-listed after concurrent AllowAlways calls")
	}
	q.ClearRules()
	if !q.RequireApproval("bash", nil) {
		t.Fatal("bash must require approval after ClearRules")
	}
}

// TestAllowAlwaysSkipsApprovalPrompt verifies the Gate integration: once a tool
// is allow-listed, the runtime executes it without publishing an
// ApprovalRequest.
func TestAllowAlwaysSkipsApprovalPrompt(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "clock"
	})
	gate.AllowAlways("clock")
	agent := newTestAgent(t, Config{Provider: scriptedToolProvider(t), Tools: reg, Gate: gate})

	events := runTurn(t, agent, "session-always", "what time is it?")
	for _, ev := range events {
		if _, ok := ev.(state.ApprovalRequest); ok {
			t.Fatalf("allow-listed tool produced an ApprovalRequest: %#v", ev)
		}
	}
	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	if got := tokenTexts(events); len(got) == 0 {
		t.Fatal("turn produced no assistant text")
	}
}
