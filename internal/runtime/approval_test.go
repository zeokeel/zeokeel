package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// collectEventsWith drains ch like collectEvents but invokes onEvent for every
// event before moving on. Tests use it to react to an ApprovalRequest while the
// turn is still running.
func collectEventsWith(t *testing.T, ch <-chan state.Event, onEvent func(state.Event)) []state.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	var events []state.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
			if onEvent != nil {
				onEvent(ev)
			}
		case <-deadline:
			t.Fatalf("timed out draining events after %d events", len(events))
		}
	}
}

// scriptedToolProvider returns a Provider that first requests the "clock" tool
// call call-1, then answers with a final text chunk on the next model call.
func scriptedToolProvider(t *testing.T) *fakeProvider {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	return &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		mu.Lock()
		call := calls
		calls++
		mu.Unlock()
		if call == 0 {
			return []Chunk{{
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "clock",
					Arguments: json.RawMessage(`{}`),
				}},
				Done: true,
			}}, nil
		}
		return []Chunk{{
			Text:  "It is 12:00.",
			Usage: &Usage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25},
			Done:  true,
		}}, nil
	}}
}

// TestProviderToolCallsDriveToolLoop verifies the M3 provider path: a Chunk
// carrying ToolCalls is surfaced through the eino model adapter, the tool runs,
// and its result is fed back into the next provider request.
func TestProviderToolCallsDriveToolLoop(t *testing.T) {
	clock := &fakeClock{result: "2026-11-04T12:00:00Z"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	p := scriptedToolProvider(t)
	agent := newTestAgent(t, Config{Provider: p, Tools: reg, ModelName: "fake-model"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-provider-tool", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}

	first, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no first request")
	}
	if len(first.Messages) != 1 || first.Messages[0].Role != RoleUser {
		t.Fatalf("first request messages = %+v", first.Messages)
	}

	second, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider got no second request")
	}
	var (
		assistantCall *Message
		toolResult    *Message
	)
	for i := range second.Messages {
		msg := &second.Messages[i]
		switch msg.Role {
		case RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				assistantCall = msg
			}
		case RoleTool:
			toolResult = msg
		}
	}
	if assistantCall == nil || len(assistantCall.ToolCalls) != 1 {
		t.Fatalf("second request has no assistant tool call: %+v", second.Messages)
	}
	call := assistantCall.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "clock" || string(call.Arguments) != "{}" {
		t.Fatalf("fed-back tool call = %+v", call)
	}
	if toolResult == nil {
		t.Fatalf("second request has no tool result: %+v", second.Messages)
	}
	if toolResult.ToolCallID != "call-1" || toolResult.Content != clock.result {
		t.Fatalf("fed-back tool result = %+v", toolResult)
	}

	if start, ok := findToolStart(events, "clock"); !ok || start.CallID != "call-1" {
		t.Fatalf("ToolStart = %+v, ok=%v", start, ok)
	}
	if end, ok := findToolEnd(events, "call-1"); !ok || end.IsError || end.Result != clock.result {
		t.Fatalf("ToolEnd = %+v, ok=%v", end, ok)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "It is 12:00." {
		t.Fatalf("text = %q", got)
	}
	usage, ok := sumUsage(events)
	if !ok || usage.Steps != 2 || usage.In != 20 || usage.Out != 5 {
		t.Fatalf("usage = %+v, ok=%v", usage, ok)
	}
}

// TestApprovalAllowWaitsThenRuns exercises the allow path: the turn blocks on
// state.ApprovalRequest until the queue approves, then the tool executes.
func TestApprovalAllowWaitsThenRuns(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "clock"
	})
	agent := newTestAgent(t, Config{Provider: scriptedToolProvider(t), Tools: reg, Gate: gate})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-approval-allow", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawRequest bool
	events := collectEventsWith(t, ch, func(ev state.Event) {
		req, ok := ev.(state.ApprovalRequest)
		if !ok {
			return
		}
		sawRequest = true
		if req.CallID != "call-1" || req.Name != "clock" || string(req.Args) != "{}" {
			t.Errorf("ApprovalRequest = %+v", req)
		}
		// The decision may land before the tool reaches Wait; the queue
		// buffers it in that case, so the return value is not asserted here.
		gate.Approve(req.CallID)
	})

	if !sawRequest {
		t.Fatal("no ApprovalRequest event")
	}
	if clock.callCount() != 1 {
		t.Fatalf("clock calls = %d, want 1", clock.callCount())
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok || end.IsError || end.Result != clock.result {
		t.Fatalf("ToolEnd = %+v, ok=%v", end, ok)
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("allow path produced error event %q", errText)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "It is 12:00." {
		t.Fatalf("text = %q", got)
	}
}

// TestApprovalDenyFailsCall verifies that denying approval never executes the
// tool, marks its ToolEnd as an error, and delivers the denial to the model as
// the call's result instead of ending the turn.
func TestApprovalDenyFailsCall(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "clock"
	})
	provider := scriptedToolProvider(t)
	agent := newTestAgent(t, Config{Provider: provider, Tools: reg, Gate: gate})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-approval-deny", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := collectEventsWith(t, ch, func(ev state.Event) {
		if req, ok := ev.(state.ApprovalRequest); ok {
			gate.Deny(req.CallID)
		}
	})

	if clock.callCount() != 0 {
		t.Fatalf("denied tool ran %d times, want 0", clock.callCount())
	}
	end, ok := findToolEnd(events, "call-1")
	if !ok {
		t.Fatalf("no ToolEnd for denied call; events: %#v", events)
	}
	if !end.IsError || !strings.Contains(end.Result, "denied by user") {
		t.Fatalf("ToolEnd = %+v", end)
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("denied tool produced a turn error %q", errText)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "It is 12:00." {
		t.Fatalf("text after denial = %q, want the next model answer", got)
	}
	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatalf("provider got no second request")
	}
	var fedBack string
	for _, msg := range second.Messages {
		if msg.Role == RoleTool && msg.ToolCallID == "call-1" {
			fedBack = msg.Content
		}
	}
	if !strings.Contains(fedBack, "denied by user") {
		t.Fatalf("second request tool result = %q, want the denial", fedBack)
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseIdle {
		t.Fatalf("phases = %v, want idle", gotPhases)
	}
}

// TestApprovalExpiresOnCancel verifies that cancelling the turn while an
// approval is pending expires the request quietly: no tool execution, no Error
// event, and the event channel still closes.
func TestApprovalExpiresOnCancel(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(toolName string, _ json.RawMessage) bool {
		return toolName == "clock"
	})
	agent := newTestAgent(t, Config{Provider: scriptedToolProvider(t), Tools: reg, Gate: gate})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := agent.Run(ctx, "session-approval-cancel", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawApproval bool
	events := collectEventsWith(t, ch, func(ev state.Event) {
		if _, ok := ev.(state.ApprovalRequest); ok {
			sawApproval = true
			cancel()
		}
	})

	if !sawApproval {
		t.Fatal("no ApprovalRequest event")
	}
	if clock.callCount() != 0 {
		t.Fatalf("expired tool ran %d times, want 0", clock.callCount())
	}
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("cancellation produced error event %q", errText)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "" {
		t.Fatalf("cancellation produced tokens %q", got)
	}
}

func TestApprovalQueue(t *testing.T) {
	var seenName string
	var seenArgs string
	q := NewApprovalQueue(func(toolName string, args json.RawMessage) bool {
		seenName = toolName
		seenArgs = string(args)
		return toolName == "bash"
	})
	if !q.RequireApproval("bash", json.RawMessage(`{"cmd":"ls"}`)) {
		t.Fatal("bash must require approval")
	}
	if seenName != "bash" || seenArgs != `{"cmd":"ls"}` {
		t.Fatalf("predicate saw %q %q", seenName, seenArgs)
	}
	if q.RequireApproval("clock", nil) {
		t.Fatal("clock must not require approval")
	}

	all := NewApprovalQueue(nil)
	if !all.RequireApproval("anything", nil) {
		t.Fatal("nil predicate must require approval for every tool")
	}

	// Allow: Wait blocks until Approve delivers the decision.
	allowResult := make(chan bool, 1)
	go func() {
		allow, err := q.Wait(context.Background(), "call-allow")
		if err != nil {
			t.Errorf("Wait allow: %v", err)
			allowResult <- false
			return
		}
		allowResult <- allow
	}()
	waitForWaiter(t, q, "call-allow")
	if !q.Approve("call-allow") {
		t.Fatal("Approve did not reach the blocked Wait")
	}
	select {
	case allow := <-allowResult:
		if !allow {
			t.Fatal("Approve produced a denial")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after Approve")
	}

	// Deny.
	denyResult := make(chan bool, 1)
	go func() {
		allow, err := q.Wait(context.Background(), "call-deny")
		if err != nil {
			t.Errorf("Wait deny: %v", err)
			denyResult <- true
			return
		}
		denyResult <- allow
	}()
	waitForWaiter(t, q, "call-deny")
	if !q.Deny("call-deny") {
		t.Fatal("Deny did not reach the blocked Wait")
	}
	select {
	case allow := <-denyResult:
		if allow {
			t.Fatal("Deny produced an allow")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after Deny")
	}

	// Early decision: resolving before Wait buffers it for the next Wait.
	if q.Approve("call-early") {
		t.Fatal("Approve for an unknown call must be buffered, not delivered")
	}
	allow, err := q.Wait(context.Background(), "call-early")
	if err != nil || !allow {
		t.Fatalf("buffered early decision = %v, %v", allow, err)
	}
	// The decision is consumed exactly once.
	expiredCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.Wait(expiredCtx, "call-early"); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Wait error = %v, want context.Canceled", err)
	}

	// Expiry: a cancelled context unblocks Wait with ErrApprovalExpired.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.Wait(ctx, "call-expired"); !errors.Is(err, ErrApprovalExpired) || !errors.Is(err, context.Canceled) {
		t.Fatalf("expired Wait error = %v", err)
	}
}

// waitForWaiter blocks briefly until the queue has registered a waiter for
// callID so tests do not race Approve/Deny against Wait's registration.
func waitForWaiter(t *testing.T, q *ApprovalQueue, callID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		_, ok := q.waiters[callID]
		q.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no waiter registered for %q", callID)
}

// TestPTCBatchEnforcesApprovalOnNestedCalls proves a PTC batch cannot bypass the
// Gate: the outer "ptc" call is not gated here, but the nested mutating call
// still publishes its own ApprovalRequest with a unique call id, and a denial
// fails that entry without invoking the tool.
func TestPTCBatchEnforcesApprovalOnNestedCalls(t *testing.T) {
	var mutCalls atomic.Int32
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "mut", invoke: func(context.Context, json.RawMessage) (string, error) {
		mutCalls.Add(1)
		return "mutated", nil
	}}); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool { return name == "mut" })
	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		if call == 0 {
			return toolCall("ptc", `{"calls":[{"tool":"mut","args":{}}]}`), nil
		}
		return finalText("done"), nil
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModePTC, Gate: gate})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "ptc-approval", "run the batch")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawNested bool
	events := collectEventsWith(t, ch, func(ev state.Event) {
		req, ok := ev.(state.ApprovalRequest)
		if !ok || req.Name != "mut" {
			return
		}
		sawNested = true
		gate.Deny(req.CallID)
	})
	if !sawNested {
		t.Fatalf("nested mut call published no approval request: %v", events)
	}
	if got := mutCalls.Load(); got != 0 {
		t.Fatalf("denied nested tool ran %d times, want 0", got)
	}
	end, ok := findToolEnd(events, "call-ptc")
	if !ok {
		t.Fatalf("ptc tool end missing: %v", events)
	}
	if end.IsError {
		t.Fatalf("ptc batch itself failed: %s", end.Result)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(end.Result), &response); err != nil {
		t.Fatalf("ptc result %q: %v", end.Result, err)
	}
	if len(response.Results) != 1 || response.Results[0].OK {
		t.Fatalf("nested results = %+v, want one denied entry", response.Results)
	}
	if !strings.Contains(response.Results[0].Error, "denied") {
		t.Fatalf("nested error = %q, want a denial", response.Results[0].Error)
	}
}

// TestPTCBatchApprovesEachNestedCall checks the allowed path uses a fresh call
// id per nested call and still runs the tool after approval.
func TestPTCBatchApprovesEachNestedCall(t *testing.T) {
	var mutCalls atomic.Int32
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "mut", invoke: func(context.Context, json.RawMessage) (string, error) {
		mutCalls.Add(1)
		return "mutated", nil
	}}); err != nil {
		t.Fatal(err)
	}
	gate := NewApprovalQueue(func(name string, _ json.RawMessage) bool { return name == "mut" })
	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		if call == 0 {
			return toolCall("ptc", `{"calls":[{"tool":"mut"},{"tool":"mut"}]}`), nil
		}
		return finalText("done"), nil
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModePTC, Gate: gate})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "ptc-approval-allow", "run the batch")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids := make(map[string]bool)
	var mu sync.Mutex
	events := collectEventsWith(t, ch, func(ev state.Event) {
		req, ok := ev.(state.ApprovalRequest)
		if !ok || req.Name != "mut" {
			return
		}
		mu.Lock()
		ids[req.CallID] = true
		mu.Unlock()
		gate.Approve(req.CallID)
	})
	if mutCalls.Load() != 2 {
		t.Fatalf("approved nested calls ran %d times, want 2", mutCalls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 2 {
		t.Fatalf("nested approval call ids = %v, want two distinct ids", ids)
	}
	end, ok := findToolEnd(events, "call-ptc")
	if !ok || end.IsError {
		t.Fatalf("ptc end = %+v, %v", end, ok)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(end.Result), &response); err != nil {
		t.Fatalf("ptc result %q: %v", end.Result, err)
	}
	for i, result := range response.Results {
		if !result.OK || result.Output != "mutated" {
			t.Fatalf("result[%d] = %+v, want mutated", i, result)
		}
	}
}
