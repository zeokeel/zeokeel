package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// memoryFixtureRows creates one memory of each interesting state and returns
// their ids so a test can assert which ones the prompt marked as used.
type memoryFixtureIDs struct {
	injectedGlobal    string
	injectedWorkspace string
	pending           string
	superseded        string
	otherWorkspace    string
}

func memoryFixtureRows(t *testing.T, st *store.Store, workspaceID, otherWorkspaceID string) memoryFixtureIDs {
	t.Helper()
	create := func(m store.Memory) string {
		t.Helper()
		row, err := st.CreateMemoryScoped(m)
		if err != nil {
			t.Fatalf("CreateMemoryScoped(%+v): %v", m, err)
		}
		return row.ID
	}
	ids := memoryFixtureIDs{
		injectedGlobal: create(store.Memory{
			Scope: store.MemoryScopeGlobal, Kind: store.MemoryKindUser,
			Status: store.MemoryStatusActive, Content: "Prefer concise answers.",
		}),
		injectedWorkspace: create(store.Memory{
			Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
			Kind: store.MemoryKindProject, Status: store.MemoryStatusActive,
			Content: "Release branch is release/2026.",
		}),
		pending: create(store.Memory{
			Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
			Kind: store.MemoryKindProject, Status: store.MemoryStatusPending,
			Content: "Pending suggestion must not be injected.",
		}),
		otherWorkspace: create(store.Memory{
			Scope: store.MemoryScopeWorkspace, WorkspaceID: otherWorkspaceID,
			Kind: store.MemoryKindProject, Status: store.MemoryStatusActive,
			Content: "Other workspace note.",
		}),
	}
	superseded := create(store.Memory{
		Scope: store.MemoryScopeWorkspace, WorkspaceID: workspaceID,
		Kind: store.MemoryKindProject, Status: store.MemoryStatusActive,
		Content: "Superseded old note.",
	})
	if err := st.RetireMemory(superseded); err != nil {
		t.Fatalf("RetireMemory: %v", err)
	}
	ids.superseded = superseded
	return ids
}

// TestMemoryPromptMarksInjectedMemoriesUsed pins REVIEW-MEMORY P2-3: building
// the instruction marks exactly the workspace+global active memories it
// injected, so last_used_at (and the importance+last_used_at eviction policy)
// reflects real prompt use instead of staying zero forever.
func TestMemoryPromptMarksInjectedMemoriesUsed(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	ws, err := st.CreateWorkspace("memory usage", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	other, err := st.CreateWorkspace("other usage", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace other: %v", err)
	}
	if err := st.SetSetting(session.SettingWorkspace, ws.ID); err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}
	ids := memoryFixtureRows(t, st, ws.ID, other.ID)

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
	instruction := bundle.Agent.Instruction()
	for _, want := range []string{"- Prefer concise answers.", "- Release branch is release/2026."} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing injected memory %q:\n%s", want, instruction)
		}
	}

	for name, id := range map[string]string{
		"injected global":    ids.injectedGlobal,
		"injected workspace": ids.injectedWorkspace,
	} {
		row, err := st.GetMemory(id)
		if err != nil {
			t.Fatalf("GetMemory(%s): %v", name, err)
		}
		if row.LastUsedAt.IsZero() {
			t.Fatalf("%s memory last_used_at is zero; the prompt did not mark it used", name)
		}
	}
	for name, id := range map[string]string{
		"pending":         ids.pending,
		"superseded":      ids.superseded,
		"other workspace": ids.otherWorkspace,
	} {
		row, err := st.GetMemory(id)
		if err != nil {
			t.Fatalf("GetMemory(%s): %v", name, err)
		}
		if !row.LastUsedAt.IsZero() {
			t.Fatalf("%s memory was marked used although it was never injected: %+v", name, row)
		}
	}
}

// TestMemoryPromptMarkFailureDoesNotBreakInstruction proves the use-marking is
// best effort: a store failure is logged and the composed instruction is still
// returned.
func TestMemoryPromptMarkFailureDoesNotBreakInstruction(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	ws, err := st.CreateWorkspace("mark failure", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := st.SetSetting(session.SettingWorkspace, ws.ID); err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}
	memoryFixtureRows(t, st, ws.ID, ws.ID)

	original := markPromptMemoriesUsed
	t.Cleanup(func() { markPromptMemoriesUsed = original })
	markPromptMemoriesUsed = func(*store.Store, []string) error {
		return errors.New("injected mark failure")
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
	instruction := bundle.Agent.Instruction()
	if !strings.Contains(instruction, "- Prefer concise answers.") ||
		!strings.Contains(instruction, "- Release branch is release/2026.") {
		t.Fatalf("instruction was lost when use-marking failed:\n%s", instruction)
	}
}
