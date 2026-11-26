package assembly_test

// The dispatch wake-up (triage → chat) is the one behaviour that exists only
// when the real modules are wired together: triage must find the chat service
// the agent module provides. Every module test of triage builds the connector by
// hand (`NewDispatcher(fakeChat, …)`), which is exactly why a product where the
// connector was silently inert stayed green for months: assigning a task to the
// lead role wrote an owner, woke nobody, and nothing failed.
//
// This test is therefore assembly-level on purpose. It loads the embedded
// product through the real registry and drives one dispatch through the services
// the root actually exposes. If triage cannot see the chat service it must fail
// here, with the reason ("the dispatcher is nil"), instead of in production where
// the only symptom is "the task has an owner and nobody is working on it".

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestAssembledTriageDispatchesToTheLead is the regression guard for the
// assembled wake-up path: the product's triage service, the product's chat
// service and the product's task board must produce a real dispatch record when
// an inbox item is assigned to the lead role.
func TestAssembledTriageDispatchesToTheLead(t *testing.T) {
	ctx := context.Background()
	root := newRoot(t)
	provideHostServices(t, root)

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{Version: "test"})
	if err != nil {
		t.Fatalf("load headless: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	inboxService, ok := root.Get(contracts.KeyInbox)
	if !ok || inboxService == nil {
		t.Fatal("KeyInbox service is not available on the root")
	}
	triageService, ok := root.Get(contracts.KeyTriage)
	if !ok || triageService == nil {
		t.Fatal("KeyTriage service is not available on the root")
	}
	chat, ok := root.Get(contracts.KeyChat)
	if !ok || chat == nil {
		t.Fatal("KeyChat service is not available on the root (the agent module provides it)")
	}
	leases, ok := root.Get(contracts.KeyTaskLease)
	if !ok || leases == nil {
		t.Fatal("KeyTaskLease service is not available on the root")
	}

	arrival, _, err := inboxService.Add(ctx, contracts.InboxItem{
		Source: "webhook", Channel: "dispatch-wiring", Sender: "owner",
		DedupKey: "dispatch-wiring", Title: "派给 lead",
	})
	if err != nil {
		t.Fatalf("inbox Add: %v", err)
	}

	result, err := triageService.Apply(ctx, arrival.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject:  "装配级派单唤醒检查",
			Detail:   "这个任务被派给 lead 角色",
			Assignee: contracts.PresetLeadID,
		},
	})
	if err != nil {
		t.Fatalf("triage Apply: %v", err)
	}
	if len(result.TaskIDs) != 1 {
		t.Fatalf("task ids = %v, want exactly one created task", result.TaskIDs)
	}

	// The assertion this file exists for. A nil record means triage never asked
	// the wake-up connector at all — on the assembled product that is the silent
	// failure where "assigned to lead" and "a lead run started" were different
	// things.
	if result.Dispatch == nil {
		t.Fatalf("dispatch = nil for a task assigned to %q: the assembled triage has no usable wake-up connector, "+
			"so the lead was never woken (triage probed %q at activation and got nothing)",
			contracts.PresetLeadID, contracts.KeyChat.Name())
	}
	if result.Dispatch.Skipped != "" {
		t.Fatalf("dispatch was skipped on a fresh assembly: %q", result.Dispatch.Skipped)
	}
	if result.Dispatch.SessionID == "" {
		t.Fatalf("dispatch = %+v, want a chat session opened for the lead run (error: %q)",
			result.Dispatch, result.Dispatch.Error)
	}
	// The session the record names is a session the real chat service really has.
	if _, exists := chat.Session(result.Dispatch.SessionID); !exists {
		t.Fatalf("dispatch session %q is not a session of the assembled chat service", result.Dispatch.SessionID)
	}
	// And it runs as the lead role: the role is what makes it an orchestration
	// run rather than a generic coding conversation.
	if setter, ok := chat.(contracts.SessionPresetSetter); ok {
		if got := setter.SessionPreset(result.Dispatch.SessionID); got != contracts.PresetLeadID {
			t.Fatalf("session role = %q, want %q", got, contracts.PresetLeadID)
		}
	} else {
		t.Fatal("the assembled chat service does not accept a session role, so dispatch cannot start a lead run")
	}
	// The spend gate ran too: a wake-up that reached the connector writes the
	// default ceiling for the task it dispatches (dispatch.go ensureBudget).
	budget, exists, err := leases.Budget(ctx, result.TaskIDs[0])
	if err != nil {
		t.Fatalf("budget of the dispatched task: %v", err)
	}
	if !exists {
		t.Fatalf("task %q has no budget row after a dispatch, so the wake-up never reached the gates", result.TaskIDs[0])
	}
	if budget.MaxTokens <= 0 || budget.MaxMinutes <= 0 {
		t.Fatalf("default dispatch budget = %+v, want positive limits on both axes", budget)
	}
}
