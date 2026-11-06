package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestMemoryToolInstructionWiring proves the production build registers the
// memory tool for the selected workspace and that a note added through the
// tool is injected by the per-turn InstructionFunc, while another workspace's
// notes stay out of the instruction.
func TestMemoryToolInstructionWiring(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	ws, err := st.CreateWorkspace("memory wiring", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	other, err := st.CreateWorkspace("other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	if err := st.SetSetting(session.SettingWorkspace, ws.ID); err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}
	if _, err := st.CreateMemory(other.ID, "Other workspace note must stay hidden."); err != nil {
		t.Fatalf("CreateMemory other: %v", err)
	}

	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		WorkspaceID: ws.ID,
		Mode:        runtime.ModeStandard,
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	if bundle == nil {
		t.Fatal("no agent bundle")
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})

	if strings.Contains(bundle.Agent.Instruction(), "Known workspace memories:") {
		t.Fatalf("memory section present before a note was added:\n%s", bundle.Agent.Instruction())
	}
	tool, ok := bundle.Agent.ToolByName("memory")
	if !ok {
		t.Fatalf("memory tool is not registered; tools: %v", bundle.Agent.ToolNames())
	}
	if _, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"action":"add","content":"Run make lint before committing."}`)); err != nil {
		t.Fatalf("memory add: %v", err)
	}

	// SetMode recomposes through InstructionFunc, the same path each turn uses.
	if err := bundle.Agent.SetMode(runtime.ModeStandard); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	instruction := bundle.Agent.Instruction()
	if !strings.Contains(instruction, "Known workspace memories:") {
		t.Fatalf("memory section missing after add:\n%s", instruction)
	}
	if !strings.Contains(instruction, "- Run make lint before committing.") {
		t.Fatalf("added memory missing from the instruction:\n%s", instruction)
	}
	if strings.Contains(instruction, "Other workspace note must stay hidden.") {
		t.Fatalf("another workspace's memory leaked into the instruction:\n%s", instruction)
	}

	// The update action is reflected on the next recomposition too.
	if _, err := tool.Invoke(context.Background(),
		json.RawMessage(`{"action":"update","id":"`+memoryIDFromList(t, st, ws.ID)+`","content":"Run go vet after lint."}`)); err != nil {
		t.Fatalf("memory update: %v", err)
	}
	if err := bundle.Agent.SetMode(runtime.ModeStandard); err != nil {
		t.Fatalf("SetMode after update: %v", err)
	}
	instruction = bundle.Agent.Instruction()
	if !strings.Contains(instruction, "- Run go vet after lint.") {
		t.Fatalf("updated memory missing from the instruction:\n%s", instruction)
	}
	if strings.Contains(instruction, "Run make lint before committing.") {
		t.Fatalf("stale memory text survived the update:\n%s", instruction)
	}

	// A bundle built for the other workspace reads only that workspace.
	otherBundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		WorkspaceID: other.ID,
		Mode:        runtime.ModeStandard,
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions(other): %v", err)
	}
	if otherBundle == nil {
		t.Fatal("no agent bundle for the other workspace")
	}
	t.Cleanup(func() {
		if otherBundle.Close != nil {
			_ = otherBundle.Close()
		}
	})
	otherInstruction := otherBundle.Agent.Instruction()
	if !strings.Contains(otherInstruction, "Other workspace note must stay hidden.") {
		t.Fatalf("other workspace's memory missing from its own instruction:\n%s", otherInstruction)
	}
	if strings.Contains(otherInstruction, "Run go vet after lint.") {
		t.Fatalf("first workspace's memory leaked into the other instruction:\n%s", otherInstruction)
	}
}

// memoryIDFromList returns the single memory id in workspaceID.
func memoryIDFromList(t *testing.T, st *store.Store, workspaceID string) string {
	t.Helper()
	list, err := st.ListMemories(workspaceID, 0)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListMemories = %d rows, want 1", len(list))
	}
	return list[0].ID
}
