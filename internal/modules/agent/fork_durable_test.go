package agent

// Tests for DSH-strict session forking: the fork starts from the source's full
// durable history (tool calls/results included, compacted windows copied as-is)
// and receives a copy of the source goal, disarmed.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestForkCopiesFullDurableHistory covers tool-call/tool-result history: the
// fork's durable history is the source's durable history, not a text-only
// projection, while the read model still carries the tool cards.
func TestForkCopiesFullDurableHistory(t *testing.T) {
	provider := &twoCallUsageProvider{
		first:  rig.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105},
		second: &rig.Usage{PromptTokens: 200, CompletionTokens: 9, TotalTokens: 209},
	}
	svc, store := newTwoCallUsageService(t, provider)
	sourceID, err := svc.NewSession("tool history")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sourceID, "use the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sourceID)
	sourceHistory, err := store.History().Load(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("history.Load(source): %v", err)
	}
	if len(sourceHistory) < 3 {
		t.Fatalf("source durable history = %d messages, want user+assistant+tool", len(sourceHistory))
	}
	hasTool := false
	for _, message := range sourceHistory {
		if message.Role == rig.RoleTool {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("source durable history lacks the tool message: %+v", sourceHistory)
	}

	newID, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forkedHistory, err := store.History().Load(context.Background(), newID)
	if err != nil {
		t.Fatalf("history.Load(fork): %v", err)
	}
	if len(forkedHistory) != len(sourceHistory) {
		t.Fatalf("fork durable history = %d messages, want the source's %d", len(forkedHistory), len(sourceHistory))
	}
	for index := range sourceHistory {
		if forkedHistory[index].Role != sourceHistory[index].Role ||
			forkedHistory[index].Text() != sourceHistory[index].Text() {
			t.Fatalf("forked message %d = %+v, want source %+v", index, forkedHistory[index], sourceHistory[index])
		}
	}
	// The read model keeps the tool cards.
	forked, ok := svc.Session(newID)
	if !ok {
		t.Fatal("forked session missing from the read model")
	}
	assistant := forked.Messages[len(forked.Messages)-1]
	if len(assistant.Tools) != 1 || assistant.Tools[0].Name != "read_file" {
		t.Fatalf("forked read-model tools = %+v, want the tool card", assistant.Tools)
	}

	// A compacted source window is copied as-is: the fork reflects what the
	// source would actually load, not the pre-compaction transcript.
	compacted := sourceHistory[:2]
	if err := store.ReplaceHistory(context.Background(), sourceID, compacted); err != nil {
		t.Fatalf("ReplaceHistory(source compacted): %v", err)
	}
	compactedFork, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession(compacted): %v", err)
	}
	compactedHistory, err := store.History().Load(context.Background(), compactedFork)
	if err != nil {
		t.Fatalf("history.Load(compacted fork): %v", err)
	}
	if len(compactedHistory) != 2 {
		t.Fatalf("compacted fork history = %d, want the compacted 2", len(compactedHistory))
	}
}

// TestForkCopiesGoalDisarmed covers the goal-copy hook: the agent invokes the
// goal backend's optional CopyGoal with the source/target session ids.
func TestForkCopiesGoalDisarmed(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	goals := newFakeGoalService("ship the fork", 5)
	svc.goalService = goals
	sourceID, err := svc.NewSession("goal source")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	newID, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	copies, from, to := goals.forkCopyCount()
	if copies != 1 || from != sourceID || to != newID {
		t.Fatalf("goal copy = %d %q->%q, want 1 %q->%q", copies, from, to, sourceID, newID)
	}
}

// TestForkWithoutGoalHasNoGoalSideEffect covers a goal-less source.
func TestForkWithoutGoalHasNoGoalSideEffect(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	goals := newFakeGoalService("objective", 5)
	goals.mu.Lock()
	goals.has = false
	goals.mu.Unlock()
	svc.goalService = goals
	sourceID, err := svc.NewSession("no goal")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := svc.ForkSession(sourceID); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if copies, _, _ := goals.forkCopyCount(); copies != 0 {
		t.Fatalf("goal copies for a goal-less source = %d, want 0", copies)
	}
}

// TestForkHistoryFallbackWithoutDurableData covers the projection fallback: a
// session whose read model has messages but whose durable history is empty (e.g.
// a manually seeded/legacy session) still forks a usable history.
func TestForkHistoryFallbackWithoutDurableData(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	sourceID, err := svc.NewSession("seeded")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	svc.sessions[sourceID].messages = []contracts.ChatMessage{
		{ID: svc.nextMessageIDLocked(), Role: contracts.ChatRoleUser, Text: "question"},
		{ID: svc.nextMessageIDLocked(), Role: contracts.ChatRoleAssistant, Text: "answer"},
	}
	svc.mu.Unlock()
	newID, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	history, err := svc.store.History().Load(context.Background(), newID)
	if err != nil {
		t.Fatalf("history.Load(fork): %v", err)
	}
	if len(history) != 2 || history[0].Text() != "question" || history[1].Text() != "answer" {
		t.Fatalf("fallback fork history = %+v, want the read-model projection", history)
	}
}
