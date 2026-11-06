package memory

import (
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Consolidation constants: superseded rows are deleted after Retention, and
// each scope keeps at most this many active rows.
const (
	// Retention is how long a superseded memory is kept as history.
	Retention = 30 * 24 * time.Hour
	// DefaultGlobalCap is the active-row cap for the global scope.
	DefaultGlobalCap = 100
	// DefaultWorkspaceCap is the active-row cap per workspace scope.
	DefaultWorkspaceCap = 300
)

// Caps overrides the per-scope active-row caps; non-positive fields use the
// defaults.
type Caps struct {
	GlobalActive    int
	WorkspaceActive int
}

// Plan is the deterministic set of consolidation operations for one pass.
// Delete IDs are removed outright; Supersede IDs are demoted to the superseded
// status by the caller. Reasons is a per-id diagnostic label.
type Plan struct {
	Delete    []string
	Supersede []string
	Reasons   map[string]string
}

// Empty reports whether the plan contains no operations.
func (p Plan) Empty() bool {
	return len(p.Delete) == 0 && len(p.Supersede) == 0
}

// ConsolidationPlan is ConsolidationPlanAt with the current time. It is the
// pure core of docs/design/auto-memory.md §5 path C: no model call, no SQL.
func ConsolidationPlan(memories []store.Memory, caps Caps) Plan {
	return ConsolidationPlanAt(memories, caps, time.Now())
}

// ConsolidationPlanAt plans one deterministic consolidation pass:
//
//   - rows with the same scope/workspace/content hash are merged: the newest
//     row survives (active beats pending on a timestamp tie), the rest are
//     deleted;
//   - superseded rows older than Retention are deleted;
//   - active rows over the global/per-workspace cap are demoted to superseded,
//     evicting the lowest importance, then oldest last_used_at/updated_at.
//
// Malformed rows (empty id, unknown scope) are ignored. The plan is sorted and
// idempotent: applying it and planning again yields an empty plan.
func ConsolidationPlanAt(memories []store.Memory, caps Caps, now time.Time) Plan {
	if caps.GlobalActive <= 0 {
		caps.GlobalActive = DefaultGlobalCap
	}
	if caps.WorkspaceActive <= 0 {
		caps.WorkspaceActive = DefaultWorkspaceCap
	}
	plan := Plan{Reasons: make(map[string]string)}
	deleted := make(map[string]bool)
	live := make([]store.Memory, 0, len(memories))
	for _, memory := range memories {
		if !usableMemory(memory) {
			continue
		}
		live = append(live, memory)
	}

	// 1) Merge exact duplicate content within one scope/workspace.
	duplicates := make(map[string][]store.Memory)
	for _, memory := range live {
		if memory.Status == store.MemoryStatusSuperseded {
			continue
		}
		key := memory.Scope + "\x00" + memory.WorkspaceID + "\x00" + rowHash(memory)
		duplicates[key] = append(duplicates[key], memory)
	}
	for _, group := range duplicates {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return betterDuplicateSurvivor(group[i], group[j])
		})
		for _, loser := range group[1:] {
			if deleted[loser.ID] {
				continue
			}
			deleted[loser.ID] = true
			plan.Delete = append(plan.Delete, loser.ID)
			plan.Reasons[loser.ID] = "duplicate content hash"
		}
	}

	// 2) Drop superseded history past its retention window.
	for _, memory := range live {
		if memory.Status != store.MemoryStatusSuperseded || memory.UpdatedAt.IsZero() {
			continue
		}
		if now.Sub(memory.UpdatedAt) <= Retention {
			continue
		}
		if deleted[memory.ID] {
			continue
		}
		deleted[memory.ID] = true
		plan.Delete = append(plan.Delete, memory.ID)
		plan.Reasons[memory.ID] = "expired superseded"
	}

	// 3) Enforce the active cap per scope/workspace, evicting the least useful
	// rows first.
	activeGroups := make(map[string][]store.Memory)
	for _, memory := range live {
		if memory.Status != store.MemoryStatusActive || deleted[memory.ID] {
			continue
		}
		key := memory.Scope + "\x00" + memory.WorkspaceID
		activeGroups[key] = append(activeGroups[key], memory)
	}
	for _, group := range activeGroups {
		limit := caps.WorkspaceActive
		if group[0].Scope == store.MemoryScopeGlobal {
			limit = caps.GlobalActive
		}
		if len(group) <= limit {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return betterActiveKeep(group[i], group[j])
		})
		for _, evict := range group[limit:] {
			if deleted[evict.ID] {
				continue
			}
			plan.Supersede = append(plan.Supersede, evict.ID)
			plan.Reasons[evict.ID] = "over active cap"
		}
	}

	sort.Strings(plan.Delete)
	sort.Strings(plan.Supersede)
	if len(plan.Delete) == 0 {
		plan.Delete = nil
	}
	if len(plan.Supersede) == 0 {
		plan.Supersede = nil
	}
	return plan
}

// usableMemory reports whether a row carries the scope/workspace pair the plan
// understands. Rows written by another schema version are ignored rather than
// guessed at.
func usableMemory(memory store.Memory) bool {
	if strings.TrimSpace(memory.ID) == "" {
		return false
	}
	switch memory.Scope {
	case store.MemoryScopeGlobal:
		return strings.TrimSpace(memory.WorkspaceID) == ""
	case store.MemoryScopeWorkspace:
		return strings.TrimSpace(memory.WorkspaceID) != ""
	default:
		return false
	}
}

// betterDuplicateSurvivor reports whether a should survive a duplicate-hash
// merge against b: newest update wins, then active over pending, then the
// higher-importance row, then a stable id order.
func betterDuplicateSurvivor(a, b store.Memory) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	if rankA, rankB := statusRank(a.Status), statusRank(b.Status); rankA != rankB {
		return rankA > rankB
	}
	if a.Importance != b.Importance {
		return a.Importance > b.Importance
	}
	return a.ID < b.ID
}

// betterActiveKeep reports whether a should be kept over b under the active cap:
// higher importance first, then most recently used, then most recently updated,
// then a stable id order.
func betterActiveKeep(a, b store.Memory) bool {
	if a.Importance != b.Importance {
		return a.Importance > b.Importance
	}
	if !a.LastUsedAt.Equal(b.LastUsedAt) {
		return a.LastUsedAt.After(b.LastUsedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID < b.ID
}
