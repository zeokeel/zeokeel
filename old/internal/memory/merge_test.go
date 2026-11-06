package memory

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// memoryRow builds a live store row for merge/consolidation tests. ContentHash
// is populated the way CreateMemoryScoped computes it.
func memoryRow(id, scope, workspaceID, content, status string, updated time.Time) store.Memory {
	return store.Memory{
		ID:          id,
		Scope:       scope,
		WorkspaceID: workspaceID,
		Kind:        KindProject,
		Status:      status,
		Content:     content,
		ContentHash: Hash(content),
		UpdatedAt:   updated,
		CreatedAt:   updated,
	}
}

func decisionsOf(decisions []Decision, action string) []Decision {
	out := make([]Decision, 0, len(decisions))
	for _, decision := range decisions {
		if decision.Action == action {
			out = append(out, decision)
		}
	}
	return out
}

func TestMergeCandidatesExactHashUpdates(t *testing.T) {
	existing := []store.Memory{
		memoryRow("m1", ScopeWorkspace, "ws-1", "The release branch is release/2026.", StatusActive, time.Unix(100, 0)),
	}
	candidates := []Candidate{{
		Scope:   ScopeWorkspace,
		Kind:    KindProject,
		Content: "  The release branch is release/2026.\n",
	}}
	decisions := MergeCandidates(existing, candidates, 0)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want one", decisions)
	}
	if decisions[0].Action != ActionUpdate || decisions[0].ExistingID != "m1" {
		t.Fatalf("decision = %+v, want update m1", decisions[0])
	}
	if decisions[0].Candidate.Content != "The release branch is release/2026." {
		t.Errorf("decision candidate = %+v, want normalized content", decisions[0].Candidate)
	}
}

func TestMergeCandidatesPendingExactHashUpdates(t *testing.T) {
	existing := []store.Memory{
		memoryRow("m-pending", ScopeGlobal, "", "Prefers small focused commits.", StatusPending, time.Unix(100, 0)),
	}
	decisions := MergeCandidates(existing, []Candidate{{Scope: ScopeGlobal, Content: "Prefers small focused commits."}}, 0)
	if len(decisions) != 1 || decisions[0].Action != ActionUpdate || decisions[0].ExistingID != "m-pending" {
		t.Fatalf("decisions = %+v, want update of the pending row", decisions)
	}
}

func TestMergeCandidatesSimilaritySupersedes(t *testing.T) {
	existing := []store.Memory{
		memoryRow("m1", ScopeWorkspace, "ws-1", "The team uses Conventional Commits for every change.", StatusActive, time.Unix(100, 0)),
		memoryRow("m2", ScopeWorkspace, "ws-1", "Unrelated project fact about the database.", StatusActive, time.Unix(200, 0)),
	}
	candidates := []Candidate{{
		Scope:   ScopeWorkspace,
		Content: "The team uses Conventional Commits for every change",
	}}
	decisions := MergeCandidates(existing, candidates, 0)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want one", decisions)
	}
	if decisions[0].Action != ActionSupersede || decisions[0].ExistingID != "m1" {
		t.Fatalf("decision = %+v, want supersede of the similar row m1", decisions[0])
	}
	if decisions[0].Similarity < SupersedeThreshold {
		t.Errorf("similarity = %v, want >= %v", decisions[0].Similarity, SupersedeThreshold)
	}
}

func TestMergeCandidatesDissimilarInserts(t *testing.T) {
	existing := []store.Memory{
		memoryRow("m1", ScopeWorkspace, "ws-1", "The database is Postgres 16.", StatusActive, time.Unix(100, 0)),
	}
	decisions := MergeCandidates(existing, []Candidate{{Content: "Releases go out on Tuesdays."}}, 0)
	if len(decisions) != 1 || decisions[0].Action != ActionInsert {
		t.Fatalf("decisions = %+v, want one insert", decisions)
	}
	if decisions[0].ExistingID != "" {
		t.Errorf("insert decision carries ExistingID %q", decisions[0].ExistingID)
	}
}

func TestMergeCandidatesScopeIsolation(t *testing.T) {
	// Same content in the other scope must not be updated or superseded.
	existing := []store.Memory{
		memoryRow("m-global", ScopeGlobal, "", "Prefers tabs for indentation.", StatusActive, time.Unix(100, 0)),
	}
	decisions := MergeCandidates(existing, []Candidate{{
		Scope:   ScopeWorkspace,
		Content: "Prefers tabs for indentation.",
	}}, 0)
	if len(decisions) != 1 || decisions[0].Action != ActionInsert {
		t.Fatalf("decisions = %+v, want insert (scopes must not mix)", decisions)
	}

	// Superseded rows are history: a matching candidate inserts instead of
	// reviving/superseding them.
	superseded := []store.Memory{
		memoryRow("m-old", ScopeWorkspace, "ws-1", "Prefers tabs for indentation.", StatusSuperseded, time.Unix(100, 0)),
	}
	decisions = MergeCandidates(superseded, []Candidate{{Content: "Prefers tabs for indentation."}}, 0)
	if len(decisions) != 1 || decisions[0].Action != ActionInsert {
		t.Fatalf("decisions = %+v, want insert over a superseded row", decisions)
	}
}

func TestMergeCandidatesBatchRules(t *testing.T) {
	existing := []store.Memory{
		memoryRow("m1", ScopeWorkspace, "ws-1", "The deploy target is staging.", StatusActive, time.Unix(100, 0)),
	}
	candidates := []Candidate{
		{Content: "The deploy target is staging."},              // update m1
		{Content: "The deploy target is staging."},              // duplicate in batch
		{Content: "Releases are tagged on the main branch."},    // insert
		{Content: "The API uses cursor pagination."},            // insert
		{Content: "The CLI binary is called zeokeel."},          // over max
		{Scope: "personal", Content: "invalid scope candidate"}, // invalid -> skip
	}
	decisions := MergeCandidates(existing, candidates, 3)
	writes := 0
	for _, decision := range decisions {
		if decision.Action != ActionSkip {
			writes++
		}
	}
	if writes > 3 {
		t.Fatalf("write decisions = %d, want <= 3: %+v", writes, decisions)
	}
	// The two entries after the cap, the in-batch duplicate and the invalid
	// candidate are skips with reasons.
	skips := decisionsOf(decisions, ActionSkip)
	if len(skips) != 3 {
		t.Fatalf("skip decisions = %+v, want 3", skips)
	}
	for _, skip := range skips {
		if skip.Reason == "" {
			t.Errorf("skip decision has no reason: %+v", skip)
		}
	}
	if got := len(decisionsOf(decisions, ActionUpdate)); got != 1 {
		t.Errorf("update decisions = %d, want 1", got)
	}
}

func TestMergeCandidatesDoesNotClaimSameRowTwice(t *testing.T) {
	existing := []store.Memory{
		memoryRow("m1", ScopeWorkspace, "ws-1", "The team uses Conventional Commits for every change.", StatusActive, time.Unix(100, 0)),
	}
	candidates := []Candidate{
		{Content: "The team uses Conventional Commits for every change."},
		{Content: "The team uses Conventional Commits for every change!"},
	}
	decisions := MergeCandidates(existing, candidates, 0)
	supersedes := 0
	for _, decision := range decisions {
		if decision.Action == ActionSupersede {
			if decision.ExistingID != "m1" {
				t.Fatalf("unexpected supersede target: %+v", decision)
			}
			supersedes++
		}
	}
	if supersedes > 1 {
		t.Fatalf("the same existing row was superseded twice: %+v", decisions)
	}
}
