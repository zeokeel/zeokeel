package memory

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/store"
)

// applyPlan simulates the store operations a caller performs for plan: deleted
// rows disappear, superseded rows are demoted with UpdatedAt moved to now.
func applyPlan(memories []store.Memory, plan Plan, now time.Time) []store.Memory {
	deleted := make(map[string]bool, len(plan.Delete))
	for _, id := range plan.Delete {
		deleted[id] = true
	}
	superseded := make(map[string]bool, len(plan.Supersede))
	for _, id := range plan.Supersede {
		superseded[id] = true
	}
	out := make([]store.Memory, 0, len(memories))
	for _, memory := range memories {
		if deleted[memory.ID] {
			continue
		}
		if superseded[memory.ID] {
			memory.Status = StatusSuperseded
			memory.UpdatedAt = now
		}
		out = append(out, memory)
	}
	return out
}

func sortedIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func TestConsolidationPlanMergesDuplicateHashes(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	old := memoryRow("m-old", ScopeWorkspace, "ws-1", "Release branch is release/2026.", StatusActive, now.Add(-48*time.Hour))
	newer := memoryRow("m-new", ScopeWorkspace, "ws-1", "  Release branch is release/2026.  ", StatusActive, now.Add(-time.Hour))
	unrelated := memoryRow("m-other", ScopeWorkspace, "ws-1", "The database is Postgres 16.", StatusActive, now.Add(-2*time.Hour))

	plan := ConsolidationPlanAt([]store.Memory{old, newer, unrelated}, Caps{}, now)
	if !reflect.DeepEqual(sortedIDs(plan.Delete), []string{"m-old"}) {
		t.Fatalf("delete = %v, want [m-old] (newest duplicate survives)", plan.Delete)
	}
	if len(plan.Supersede) != 0 {
		t.Fatalf("supersede = %v, want none", plan.Supersede)
	}
	if plan.Reasons["m-old"] == "" {
		t.Error("duplicate delete has no reason")
	}
}

func TestConsolidationPlanKeepsScopesSeparate(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	rows := []store.Memory{
		memoryRow("g", ScopeGlobal, "", "Same content.", StatusActive, now.Add(-time.Hour)),
		memoryRow("w1", ScopeWorkspace, "ws-1", "Same content.", StatusActive, now.Add(-2*time.Hour)),
		memoryRow("w2", ScopeWorkspace, "ws-2", "Same content.", StatusActive, now.Add(-3*time.Hour)),
	}
	plan := ConsolidationPlanAt(rows, Caps{}, now)
	if !plan.Empty() {
		t.Fatalf("plan = %+v, want no operations across scopes/workspaces", plan)
	}
}

func TestConsolidationPlanExpiresSupersededRows(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	rows := []store.Memory{
		memoryRow("expired", ScopeWorkspace, "ws-1", "Old note.", StatusSuperseded, now.Add(-Retention-time.Hour)),
		memoryRow("recent", ScopeWorkspace, "ws-1", "Recent note.", StatusSuperseded, now.Add(-Retention+time.Hour)),
		memoryRow("unknown-age", ScopeWorkspace, "ws-1", "No timestamp.", StatusSuperseded, time.Time{}),
	}
	plan := ConsolidationPlanAt(rows, Caps{}, now)
	if !reflect.DeepEqual(sortedIDs(plan.Delete), []string{"expired"}) {
		t.Fatalf("delete = %v, want [expired] only", plan.Delete)
	}
	if plan.Reasons["expired"] == "" {
		t.Error("expired delete has no reason")
	}
}

func TestConsolidationPlanEvictsOverCap(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	high := memoryRow("high", ScopeGlobal, "", "High importance note.", StatusActive, now.Add(-time.Hour))
	high.Importance = 5
	high.LastUsedAt = now.Add(-time.Hour)
	lowOld := memoryRow("low-old", ScopeGlobal, "", "Low importance, stale note.", StatusActive, now.Add(-30*24*time.Hour))
	lowOld.Importance = 1
	lowOld.LastUsedAt = now.Add(-30 * 24 * time.Hour)
	lowNew := memoryRow("low-new", ScopeGlobal, "", "Low importance, recently used.", StatusActive, now.Add(-2*time.Hour))
	lowNew.Importance = 1
	lowNew.LastUsedAt = now.Add(-2 * time.Hour)

	plan := ConsolidationPlanAt([]store.Memory{high, lowOld, lowNew}, Caps{GlobalActive: 2}, now)
	if !reflect.DeepEqual(sortedIDs(plan.Supersede), []string{"low-old"}) {
		t.Fatalf("supersede = %v, want [low-old] (lowest importance, then oldest last_used_at)", plan.Supersede)
	}
	if plan.Reasons["low-old"] == "" {
		t.Error("cap eviction has no reason")
	}
}

func TestConsolidationPlanCapsWorkspacesSeparately(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	wsOneA := memoryRow("ws1-a", ScopeWorkspace, "ws-1", "First workspace note A.", StatusActive, now.Add(-time.Hour))
	wsOneB := memoryRow("ws1-b", ScopeWorkspace, "ws-1", "First workspace note B.", StatusActive, now.Add(-2*time.Hour))
	wsTwo := memoryRow("ws2-a", ScopeWorkspace, "ws-2", "Second workspace note.", StatusActive, now.Add(-3*time.Hour))
	plan := ConsolidationPlanAt([]store.Memory{wsOneA, wsOneB, wsTwo}, Caps{WorkspaceActive: 1}, now)
	// ws-1 has two rows (one evicted), ws-2 has one row (exactly at cap).
	if len(plan.Supersede) != 1 {
		t.Fatalf("supersede = %v, want one eviction", plan.Supersede)
	}
	if plan.Supersede[0] != "ws1-b" {
		t.Fatalf("supersede = %v, want the older ws-1 row", plan.Supersede)
	}
}

func TestConsolidationPlanDefaultsCaps(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	rows := make([]store.Memory, 0, DefaultGlobalCap+1)
	for i := 0; i <= DefaultGlobalCap; i++ {
		id := "g-" + itoa(i)
		row := memoryRow(id, ScopeGlobal, "", "Global note "+id, StatusActive, now.Add(-time.Duration(i)*time.Minute))
		row.Importance = 1
		rows = append(rows, row)
	}
	plan := ConsolidationPlanAt(rows, Caps{}, now)
	if len(plan.Supersede) != 1 {
		t.Fatalf("supersede = %v, want exactly one over the default global cap %d", plan.Supersede, DefaultGlobalCap)
	}
	if plan.Supersede[0] != "g-"+itoa(DefaultGlobalCap) {
		t.Fatalf("supersede = %v, want the oldest global row", plan.Supersede)
	}

	// At the cap: no operation.
	plan = ConsolidationPlanAt(rows[:DefaultGlobalCap], Caps{}, now)
	if !plan.Empty() {
		t.Fatalf("plan = %+v, want empty at exactly the cap", plan)
	}
}

func TestConsolidationPlanDeterministicAndIdempotent(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	rows := []store.Memory{
		memoryRow("dup-1", ScopeWorkspace, "ws-1", "Duplicated note.", StatusActive, now.Add(-4*time.Hour)),
		memoryRow("dup-2", ScopeWorkspace, "ws-1", "Duplicated note.", StatusActive, now.Add(-3*time.Hour)),
		memoryRow("expired", ScopeWorkspace, "ws-1", "Expired note.", StatusSuperseded, now.Add(-Retention-time.Hour)),
	}
	first := ConsolidationPlanAt(rows, Caps{WorkspaceActive: 1}, now)
	second := ConsolidationPlanAt(rows, Caps{WorkspaceActive: 1}, now)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("plan is not deterministic:\n%+v\n%+v", first, second)
	}
	after := applyPlan(rows, first, now)
	if replanned := ConsolidationPlanAt(after, Caps{WorkspaceActive: 1}, now); !replanned.Empty() {
		t.Fatalf("plan is not idempotent, second plan = %+v", replanned)
	}
}

func TestConsolidationPlanIgnoresUnknownRows(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	rows := []store.Memory{
		{ID: "", Scope: ScopeWorkspace, WorkspaceID: "ws-1", Content: "No id.", Status: StatusActive},
		{ID: "odd", Scope: "bogus", WorkspaceID: "ws-1", Content: "Odd scope.", Status: StatusActive, ContentHash: Hash("Odd scope.")},
	}
	plan := ConsolidationPlanAt(rows, Caps{}, now)
	if !plan.Empty() {
		t.Fatalf("plan = %+v, want unknown rows ignored", plan)
	}
}
