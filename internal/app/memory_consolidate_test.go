package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/store"
)

// memoryRowForTest builds a scoped memory row for consolidation tests.
func memoryRowForTest(scope, workspaceID, content string) store.Memory {
	return store.Memory{
		Scope:       scope,
		WorkspaceID: workspaceID,
		Kind:        store.MemoryKindProject,
		Source:      store.MemorySourceAuto,
		Status:      store.MemoryStatusActive,
		Content:     content,
		Confidence:  0.8,
	}
}

func itoaMemoryTest(n int) string { return strconv.Itoa(n) }

func TestConsolidateMemoriesMergesDuplicatesAndIsIdempotent(t *testing.T) {
	st, dir := newToolStore(t)
	workspace, err := st.CreateWorkspace("consolidate", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	// The legacy append API does not deduplicate, which mirrors rows written
	// before schema v3 (or by a crashed pipeline run).
	for i := 0; i < 2; i++ {
		if _, err := st.CreateMemory(workspace.ID, "Duplicate note for consolidation."); err != nil {
			t.Fatalf("CreateMemory(%d): %v", i, err)
		}
	}
	plan, err := ConsolidateMemories(context.Background(), st)
	if err != nil {
		t.Fatalf("ConsolidateMemories: %v", err)
	}
	if len(plan.Delete) != 1 {
		t.Fatalf("plan.Delete = %v, want one duplicate", plan.Delete)
	}
	rows, err := st.ListMemoriesScoped("workspace", workspace.ID, "", 10)
	if err != nil {
		t.Fatalf("ListMemoriesScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows after consolidation = %+v, want one", rows)
	}
	// Idempotent: a second pass has nothing to do.
	again, err := ConsolidateMemories(context.Background(), st)
	if err != nil {
		t.Fatalf("second ConsolidateMemories: %v", err)
	}
	if !again.Empty() {
		t.Fatalf("second plan = %+v, want empty", again)
	}
}

func TestConsolidateMemoriesEnforcesGlobalCap(t *testing.T) {
	st, _ := newToolStore(t)
	// Default global cap is 100 active rows; create one over it.
	for i := 0; i <= 100; i++ {
		content := "Global consolidation note " + itoaMemoryTest(i)
		if _, err := st.CreateMemoryScoped(memoryRowForTest("global", "", content)); err != nil {
			t.Fatalf("CreateMemoryScoped(%d): %v", i, err)
		}
	}
	plan, err := ConsolidateMemories(context.Background(), st)
	if err != nil {
		t.Fatalf("ConsolidateMemories: %v", err)
	}
	if len(plan.Supersede) != 1 {
		t.Fatalf("plan.Supersede = %v, want one over-cap eviction", plan.Supersede)
	}
	active, err := st.ListMemoriesScoped("global", "", "active", 200)
	if err != nil {
		t.Fatalf("ListMemoriesScoped: %v", err)
	}
	if len(active) != 100 {
		t.Fatalf("global active rows = %d, want 100", len(active))
	}
	// Idempotent at the cap.
	again, err := ConsolidateMemories(context.Background(), st)
	if err != nil {
		t.Fatalf("second ConsolidateMemories: %v", err)
	}
	if !again.Empty() {
		t.Fatalf("second plan = %+v, want empty", again)
	}
}

func TestConsolidateMemoriesNoOpCases(t *testing.T) {
	plan, err := ConsolidateMemories(context.Background(), nil)
	if err != nil || !plan.Empty() {
		t.Fatalf("nil store = (%+v, %v), want empty/nil", plan, err)
	}
	st, _ := newToolStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConsolidateMemories(ctx, st); err == nil {
		t.Fatal("cancelled consolidation returned no error")
	}
}

func TestMemoryConsolidationLoopRunsAndStops(t *testing.T) {
	st, dir := newToolStore(t)
	workspace, err := st.CreateWorkspace("loop", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := st.CreateMemory(workspace.ID, "Loop duplicate note."); err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	if _, err := st.CreateMemory(workspace.ID, "Loop duplicate note."); err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		memoryConsolidationLoop(ctx, st, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := st.ListMemoriesScoped("workspace", workspace.ID, "", 10)
		if err == nil && len(rows) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("consolidation loop did not stop on context cancellation")
	}
	rows, err := st.ListMemoriesScoped("workspace", workspace.ID, "", 10)
	if err != nil {
		t.Fatalf("ListMemoriesScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("loop did not consolidate the duplicate: %+v", rows)
	}
}
