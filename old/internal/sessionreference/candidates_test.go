package sessionreference_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
)

// candidateFixture is the fixed session list the discovery tests rank.
func candidateFixture() []sessionreference.Session {
	return []sessionreference.Session{
		{ID: "a", Cwd: "/work", Title: "Alpha plan"},
		{ID: "b", Cwd: "/other", Title: ""},
		{ID: "c", Cwd: "/work", Title: "alpha two"},
		{ID: "d", Cwd: "", Title: "delta"},
		{ID: "e", Cwd: "/other", Title: "Subagent", DisplayTitle: "Agent label"},
	}
}

func candidateIDs(candidates []sessionreference.Candidate) []string {
	out := make([]string, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate.SessionID
	}
	return out
}

// TestCandidatesRanksSameCwdFirst pins DSH's discovery order: sessions whose
// working directory matches the caller's come first, sessions without a
// directory next, the rest after, each group keeping its stable input order.
func TestCandidatesRanksSameCwdFirst(t *testing.T) {
	got, err := sessionreference.Candidates("/work", "", 10, candidateFixture())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := []string{"a", "c", "d", "b", "e"}
	if ids := candidateIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("Candidates order = %v, want %v (same cwd, no cwd, others)", ids, want)
	}
	if !got[0].SameWorkspace || !got[1].SameWorkspace {
		t.Fatalf("same-workspace flags = %+v, want true for a and c", got[:2])
	}
	if got[2].SameWorkspace || got[3].SameWorkspace || got[4].SameWorkspace {
		t.Fatalf("same-workspace flags = %+v, want false for the rest", got[2:])
	}
}

// TestCandidatesLabelsFallBack covers DSH's label rule: the latest title, or
// the session id when the title is missing, with the display title falling back
// to that label.
func TestCandidatesLabelsFallBack(t *testing.T) {
	got, err := sessionreference.Candidates("/work", "", 10, candidateFixture())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	byID := make(map[string]sessionreference.Candidate, len(got))
	for _, candidate := range got {
		byID[candidate.SessionID] = candidate
	}
	if byID["a"].Label != "Alpha plan" || byID["a"].DisplayTitle != "Alpha plan" {
		t.Fatalf("title-backed candidate = %+v", byID["a"])
	}
	if byID["b"].Label != "b" || byID["b"].DisplayTitle != "b" {
		t.Fatalf("untitled candidate must fall back to the id: %+v", byID["b"])
	}
	if byID["e"].Label != "Subagent" || byID["e"].DisplayTitle != "Agent label" {
		t.Fatalf("display-title candidate = %+v", byID["e"])
	}
}

// TestCandidatesFilter is the case-insensitive substring search over id, cwd,
// title and display title.
func TestCandidatesFilter(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"ALPHA", []string{"a", "c"}},
		{"/OTHER", []string{"b", "e"}},
		{"agent", []string{"e"}},
		{"zzz", nil},
	}
	for _, tc := range cases {
		got, err := sessionreference.Candidates("/work", tc.query, 10, candidateFixture())
		if err != nil {
			t.Fatalf("Candidates(%q): %v", tc.query, err)
		}
		ids := candidateIDs(got)
		if len(ids) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Errorf("Candidates(%q) ids = %v, want %v", tc.query, ids, tc.want)
		}
	}
}

// TestCandidatesLimitAndValidation covers the positive limit contract.
func TestCandidatesLimitAndValidation(t *testing.T) {
	got, err := sessionreference.Candidates("/work", "", 2, candidateFixture())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if want := []string{"a", "c"}; !reflect.DeepEqual(candidateIDs(got), want) {
		t.Fatalf("Candidates limit ids = %v, want %v", candidateIDs(got), want)
	}
	for _, limit := range []int{0, -1} {
		if _, err := sessionreference.Candidates("/work", "", limit, candidateFixture()); err == nil {
			t.Errorf("Candidates(limit=%d) error = nil, want an explicit error", limit)
		} else {
			var refErr *sessionreference.Error
			if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeInvalidReference {
				t.Errorf("Candidates(limit=%d) error = %v, want code %s", limit, err, sessionreference.CodeInvalidReference)
			}
		}
	}
}
