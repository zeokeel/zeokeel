package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// turnError returns the first state.Error published by a turn, or nil.
func turnError(events []state.Event) error {
	for _, ev := range events {
		if e, ok := ev.(state.Error); ok {
			return e.Err
		}
	}
	return nil
}

// requireToolRoundTrip asserts that events contain a successful ToolStart /
// ToolEnd pair for name and that the turn's answer contains wantText.
func requireToolRoundTrip(t *testing.T, label string, events []state.Event, name, wantText string) {
	t.Helper()
	start, ok := findToolStart(events, name)
	if !ok {
		t.Fatalf("%s: %s ToolStart missing from events: %v", label, name, events)
	}
	end, ok := findToolEnd(events, start.CallID)
	if !ok {
		t.Fatalf("%s: %s ToolEnd missing from events: %v", label, name, events)
	}
	if end.IsError || end.Result != name+" ok" {
		t.Fatalf("%s: %s ToolEnd = %+v, want the pinned topology's result", label, name, end)
	}
	if tokens := strings.Join(tokenTexts(events), ""); !strings.Contains(tokens, wantText) {
		t.Fatalf("%s: tokens = %q, want %q", label, tokens, wantText)
	}
}

// TestModeSwitchMidTurnKeepsPinnedToolSet is the regression for a narrowing
// mode switch during an in-flight turn. A Standard turn blocks before its bash
// tool call arrives; switching the shared agent to Minimal used to make
// handleToolCalls validate "bash" against the live Minimal tool set and abort
// the turn with `model requested unknown tool "bash"`. The turn must keep
// running against the topology (mode and strictness) it pinned at Run time.
func TestModeSwitchMidTurnKeepsPinnedToolSet(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"bash", "terminal_open", "read_image", "working_directory"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	started := make(chan struct{})
	release := make(chan struct{})
	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		switch call {
		case 0:
			// Hold the turn before its bash tool call so the test can switch
			// the mode while the turn is definitely in flight.
			close(started)
			<-release
			return toolCall("bash", `{"command":"echo ok"}`), nil
		case 1:
			return finalText("bash done"), nil
		}
		return finalText("unexpected call"), nil
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModeStandard})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "pinned-midturn", "run bash")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-started

	if err := agent.SetMode(ModeMinimal); err != nil {
		t.Fatalf("SetMode(minimal) during turn: %v", err)
	}
	if got := agent.Mode(); got != ModeMinimal {
		t.Fatalf("Agent.Mode() after switch = %q, want minimal", got)
	}
	if _, ok := agent.ToolByName("bash"); ok {
		t.Fatalf("live minimal tools still expose bash: %v", agent.ToolNames())
	}
	close(release)

	events := collectEvents(t, ch)
	if err := turnError(events); err != nil {
		t.Fatalf("pinned Standard turn failed after switching to minimal: %v (events: %v)", err, events)
	}
	requireToolRoundTrip(t, "mid-turn standard", events, "bash", "bash done")
}

// TestModeSwitchBeforeQueuedTurnKeepsPinnedToolSet covers the other race: a
// turn queued behind a running one pins its topology when Run is called, then
// the mode changes before it acquires runMu. Its pinned Standard tool set must
// still accept the bash call it emits once the first turn releases the lock.
func TestModeSwitchBeforeQueuedTurnKeepsPinnedToolSet(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"bash", "terminal_open", "read_image", "working_directory"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	started := make(chan struct{})
	release := make(chan struct{})
	m := newScriptModel(func(call int) ([]*schema.Message, error) {
		switch call {
		case 0:
			close(started)
			<-release
			return finalText("first turn done"), nil
		case 1:
			return toolCall("bash", `{}`), nil
		case 2:
			return finalText("queued turn done"), nil
		}
		return finalText("unexpected call"), nil
	})
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModeStandard})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, err := agent.Run(ctx, "pinned-queued-first", "first")
	if err != nil {
		t.Fatalf("Run(first): %v", err)
	}
	<-started

	// The first turn holds runMu, so the second Run returns with its turn
	// queued; Run pinned the Standard state before returning.
	second, err := agent.Run(ctx, "pinned-queued-second", "second")
	if err != nil {
		t.Fatalf("Run(second): %v", err)
	}
	if err := agent.SetMode(ModeMinimal); err != nil {
		t.Fatalf("SetMode(minimal) while the second turn is queued: %v", err)
	}
	if got := agent.Mode(); got != ModeMinimal {
		t.Fatalf("Agent.Mode() after switch = %q, want minimal", got)
	}
	close(release)

	firstEvents := collectEvents(t, first)
	if err := turnError(firstEvents); err != nil {
		t.Fatalf("first turn failed: %v (events: %v)", err, firstEvents)
	}
	secondEvents := collectEvents(t, second)
	if err := turnError(secondEvents); err != nil {
		t.Fatalf("queued Standard turn failed after switching to minimal: %v (events: %v)", err, secondEvents)
	}
	requireToolRoundTrip(t, "queued standard", secondEvents, "bash", "queued turn done")
	// The queued turn really did run after the switch (it is pinned, not
	// live): the model still saw the Standard tool set for its call.
	if tools := m.toolNamesAt(1); !hasTool(tools, "bash") {
		t.Fatalf("queued turn model tools = %v, want the pinned Standard set", tools)
	}
}
