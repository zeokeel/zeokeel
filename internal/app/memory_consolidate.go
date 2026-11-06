package app

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/zeokeel/zeokeel/internal/memory"
	"github.com/zeokeel/zeokeel/internal/store"
)

// Memory consolidation (docs/design/auto-memory.md §5 path C).
//
// Consolidation is deterministic and model-free: it merges exact duplicate
// content, deletes superseded history past its retention window and demotes the
// least useful active rows when a scope exceeds its cap. It runs at app startup
// and every six hours, and after enough automatic writes. Applying the plan is
// idempotent, so overlapping runs are harmless.

// memoryConsolidationInterval is the periodic consolidation cadence.
const memoryConsolidationInterval = 6 * time.Hour

// ConsolidateMemories plans and applies one consolidation pass over every
// scope: global plus each stored workspace. It returns the applied plan (for
// logging/diagnostics); a nil store or an already-cancelled context is a
// no-op.
func ConsolidateMemories(ctx context.Context, st *store.Store) (memory.Plan, error) {
	plan := memory.Plan{}
	if st == nil {
		return plan, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	rows, err := collectMemoryRows(st)
	if err != nil {
		return plan, err
	}
	// The per-scope read API returns at most store.MaxMemoryLimit rows. That
	// bound is above both caps, so a normal pass sees every active row it may
	// need to evict.
	plan = memory.ConsolidationPlanAt(rows, memory.Caps{
		GlobalActive:    memory.DefaultGlobalCap,
		WorkspaceActive: memory.DefaultWorkspaceCap,
	}, time.Now())
	for _, id := range plan.Delete {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		if err := st.DeleteMemory(id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return plan, err
		}
	}
	for _, id := range plan.Supersede {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		// A cap eviction has no successor row: RetireMemory demotes it to
		// superseded with an empty superseded_by, so the Settings list renders
		// it as history instead of a self-reference.
		if err := st.RetireMemory(id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return plan, err
		}
	}
	return plan, nil
}

// collectMemoryRows reads the rows consolidation can see: every status of the
// global scope plus every status of each stored workspace.
func collectMemoryRows(st *store.Store) ([]store.Memory, error) {
	rows, err := st.ListMemoriesScoped(store.MemoryScopeGlobal, "", "", store.MaxMemoryLimit)
	if err != nil {
		return nil, err
	}
	workspaces, err := st.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		scoped, err := st.ListMemoriesScoped(store.MemoryScopeWorkspace, workspace.ID, "", store.MaxMemoryLimit)
		if err != nil {
			return nil, err
		}
		rows = append(rows, scoped...)
	}
	return rows, nil
}

// memoryConsolidationLoop runs one consolidation pass immediately (startup)
// and then on every interval tick until ctx is cancelled.
func memoryConsolidationLoop(ctx context.Context, st *store.Store, interval time.Duration) {
	if interval <= 0 {
		interval = memoryConsolidationInterval
	}
	run := func() {
		if ctx.Err() != nil {
			return
		}
		if _, err := ConsolidateMemories(ctx, st); err != nil && ctx.Err() == nil {
			log.Printf("zeokeel: memory consolidation: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
