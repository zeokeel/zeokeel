package gateway

// Coalescing is per domain key (docs/TRANSPORT.md §4.5): while a frame is still
// waiting to be written, a newer delta for the same domain is merged into it.
// The key has to be specific or the merge crosses domains — AUDIT-2026-11-24
// A-2: per-session deltas keyed by notification name (or falling back to "?")
// let one session's frame swallow another's, and a frame that carried the wrong
// sessionId is then routed to the wrong conversation by the client.

import "testing"

// pendingFrames drains the client's queue as the deltas it currently holds.
func pendingFrames(c *client) []deltaPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []deltaPayload
	for _, frame := range c.queue {
		if frame.delta != nil {
			out = append(out, frame.delta)
		}
	}
	return out
}

// TestCoalescingKeepsPerSessionDeltasApart covers the two shapes that collided:
// the prompt inbox (no case at all) and the task boards (both `tasks.<id>`).
func TestCoalescingKeepsPerSessionDeltasApart(t *testing.T) {
	gw := &Gateway{}
	c := newClient(gw, nil)

	c.enqueueDelta(&promptsDelta{
		SessionID: "a", Rev: 1,
		Added: []queuedPromptDTO{{ID: "p-a", Text: "A"}},
	})
	c.enqueueDelta(&promptsDelta{
		SessionID: "b", Rev: 1,
		Added: []queuedPromptDTO{{ID: "p-b", Text: "B"}},
	})
	frames := pendingFrames(c)
	if len(frames) != 2 {
		t.Fatalf("prompts deltas for two sessions coalesced into %d frame(s), want 2", len(frames))
	}
	seen := map[string]string{}
	for _, frame := range frames {
		delta, ok := frame.(*promptsDelta)
		if !ok {
			t.Fatalf("frame = %T, want *promptsDelta", frame)
		}
		for _, item := range delta.Added {
			seen[item.ID] = delta.SessionID
		}
	}
	if seen["p-a"] != "a" || seen["p-b"] != "b" {
		t.Fatalf("prompt routed to the wrong session: %v", seen)
	}
}

// TestCoalescingKeepsTaskBoardsApart covers the same collision between the
// process-wide board and one conversation's board: both speak `tasks.delta`.
func TestCoalescingKeepsTaskBoardsApart(t *testing.T) {
	gw := &Gateway{}
	c := newClient(gw, nil)

	c.enqueueDelta(&itemSetDelta{
		method: "tasks.delta", domain: domainTasks, Rev: 1,
		Added: []any{taskDTO{ID: "global-task"}},
	})
	c.enqueueDelta(&itemSetDelta{
		method: "tasks.delta", domain: "tasks:s1", Rev: 1,
		Added: []any{taskDTO{ID: "session-task"}},
	})
	frames := pendingFrames(c)
	if len(frames) != 2 {
		t.Fatalf("task deltas for the two boards coalesced into %d frame(s), want 2", len(frames))
	}
	for _, frame := range frames {
		delta, ok := frame.(*itemSetDelta)
		if !ok {
			t.Fatalf("frame = %T, want *itemSetDelta", frame)
		}
		for _, item := range delta.Added {
			task, _ := item.(taskDTO)
			if delta.domain == domainTasks && task.ID == "session-task" {
				t.Fatalf("a conversation task leaked into the process-wide board frame")
			}
			if delta.domain == "tasks:s1" && task.ID == "global-task" {
				t.Fatalf("a process-wide task leaked into the conversation board frame")
			}
		}
	}
}

// TestCoalescingStillMergesOneDomain guards the opposite mistake: making the key
// so specific that coalescing stops working. Two deltas for the same session
// must still produce one frame.
func TestCoalescingStillMergesOneDomain(t *testing.T) {
	gw := &Gateway{}
	c := newClient(gw, nil)
	c.enqueueDelta(&sessionDelta{SessionID: "s1", Rev: 1, Ops: []sessionOp{{Op: opSessionPatch, Fields: map[string]any{"running": true}}}})
	c.enqueueDelta(&sessionDelta{SessionID: "s1", Rev: 2, Ops: []sessionOp{{Op: opSessionPatch, Fields: map[string]any{"running": false}}}})
	frames := pendingFrames(c)
	if len(frames) != 1 {
		t.Fatalf("same-session deltas made %d frames, want 1 (coalescing broke)", len(frames))
	}
	delta, _ := frames[0].(*sessionDelta)
	if delta == nil || delta.Rev != 2 {
		t.Fatalf("merged delta = %+v, want rev 2", frames[0])
	}
}
