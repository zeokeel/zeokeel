package contracts

// Table-driven tests for the session-reference candidates, URI codec and
// budget errors (TDD: written before the implementation).

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func refCandidate(id, title string, score int, updated time.Time) SessionReferenceCandidate {
	return SessionReferenceCandidate{SessionID: id, Title: title, Score: score, UpdatedAt: updated}
}

func candidateIDs(candidates []SessionReferenceCandidate) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.SessionID)
	}
	return ids
}

func TestRankSessionReferenceCandidates(t *testing.T) {
	base := time.Unix(1700000000, 0)
	candidates := []SessionReferenceCandidate{
		refCandidate("deepseek", "Other title", 0, base.Add(3*time.Hour)),
		refCandidate("session-1", "DeepSeek notes", 5, base.Add(2*time.Hour)),
		refCandidate("deepseek-2", "prefix id", 0, base.Add(time.Hour)),
		refCandidate("other", "nothing", 0, base.Add(4*time.Hour)),
	}

	// Exact ID first, then ID prefix, then title contains; non-matches dropped.
	ranked := RankSessionReferenceCandidates(candidates, "deepseek", 8)
	if got := candidateIDs(ranked); len(got) != 3 || got[0] != "deepseek" ||
		got[1] != "deepseek-2" || got[2] != "session-1" {
		t.Fatalf("ranked = %v", got)
	}

	// Query matching is case-insensitive.
	ranked = RankSessionReferenceCandidates(candidates, "DEEPSEEK", 8)
	if got := candidateIDs(ranked); len(got) != 3 || got[0] != "deepseek" {
		t.Fatalf("case-insensitive ranked = %v", got)
	}

	// Empty query keeps everything, newest first.
	ranked = RankSessionReferenceCandidates(candidates, "  ", 8)
	if got := candidateIDs(ranked); len(got) != 4 || got[0] != "other" ||
		got[1] != "deepseek" || got[2] != "session-1" || got[3] != "deepseek-2" {
		t.Fatalf("empty-query ranked = %v", got)
	}

	// Title matches within one class order by score, then recency.
	titleCandidates := []SessionReferenceCandidate{
		refCandidate("t-old", "alpha notes", 0, base),
		refCandidate("t-new", "alpha plan", 0, base.Add(time.Hour)),
		refCandidate("t-scored", "alpha doc", 9, base),
	}
	ranked = RankSessionReferenceCandidates(titleCandidates, "alpha", 8)
	if got := candidateIDs(ranked); len(got) != 3 || got[0] != "t-scored" ||
		got[1] != "t-new" || got[2] != "t-old" {
		t.Fatalf("title ranking = %v", got)
	}

	// No match -> empty; limit truncates; limit<=0 defaults to 8.
	if got := RankSessionReferenceCandidates(candidates, "zzz", 8); len(got) != 0 {
		t.Fatalf("no-match ranked = %v", got)
	}
	many := make([]SessionReferenceCandidate, 0, 12)
	for index := 0; index < 12; index++ {
		many = append(many, refCandidate("id-"+string(rune('a'+index)), "x", 0,
			base.Add(time.Duration(index)*time.Minute)))
	}
	if got := RankSessionReferenceCandidates(many, "", 3); len(got) != 3 {
		t.Fatalf("limit ranked = %v", candidateIDs(got))
	}
	if got := RankSessionReferenceCandidates(many, "", 0); len(got) != 8 {
		t.Fatalf("default limit ranked = %d, want 8", len(got))
	}

	// Duplicate session IDs are collapsed to one entry.
	dupes := []SessionReferenceCandidate{
		refCandidate("same", "first", 0, base),
		refCandidate("same", "second", 3, base.Add(time.Hour)),
	}
	if got := RankSessionReferenceCandidates(dupes, "", 8); len(got) != 1 || got[0].Title != "second" {
		t.Fatalf("deduped ranked = %+v", got)
	}
}

func TestSessionReferenceURIRoundTrip(t *testing.T) {
	id := "sess-1"
	uri, err := EncodeSessionReferenceURI(id)
	if err != nil || uri != "sessionref:sess-1" {
		t.Fatalf("Encode(%q) = %q, %v", id, uri, err)
	}
	decoded, err := DecodeSessionReferenceURI(uri)
	if err != nil || decoded != id {
		t.Fatalf("Decode(%q) = %q, %v", uri, decoded, err)
	}

	// Path punctuation is percent-encoded and decoded back.
	complexID := "team/a.b"
	uri, err = EncodeSessionReferenceURI(complexID)
	if err != nil {
		t.Fatalf("Encode(%q): %v", complexID, err)
	}
	if !strings.HasPrefix(uri, "sessionref:") || strings.Contains(uri, "/") {
		t.Fatalf("Encode(%q) = %q, want an escaped sessionref URI", complexID, uri)
	}
	decoded, err = DecodeSessionReferenceURI(uri)
	if err != nil || decoded != complexID {
		t.Fatalf("Decode(%q) = %q, %v", uri, decoded, err)
	}

	// Bare IDs stay supported for compatibility.
	decoded, err = DecodeSessionReferenceURI("  sess-1  ")
	if err != nil || decoded != "sess-1" {
		t.Fatalf("Decode(bare) = %q, %v", decoded, err)
	}
}

func TestSessionReferenceURIErrors(t *testing.T) {
	if _, err := EncodeSessionReferenceURI(""); err == nil {
		t.Fatal("Encode(empty) succeeded")
	}
	if _, err := EncodeSessionReferenceURI("bad id"); err == nil {
		t.Fatal("Encode(id with space) succeeded")
	}
	if _, err := EncodeSessionReferenceURI("bad\nid"); err == nil {
		t.Fatal("Encode(id with control char) succeeded")
	}
	for _, raw := range []string{"", "   ", "sessionref:", "sessionref:%zz", "bad id"} {
		if _, err := DecodeSessionReferenceURI(raw); err == nil {
			t.Fatalf("Decode(%q) succeeded", raw)
		}
	}
}

func TestSessionReferenceBudget(t *testing.T) {
	if err := CheckSessionReferenceBudget(0, 0); err != nil {
		t.Fatalf("zero budget = %v", err)
	}
	if err := CheckSessionReferenceBudget(3, 100); err != nil {
		t.Fatalf("positive budgets = %v", err)
	}

	err := CheckSessionReferenceBudget(-2, 100)
	if err == nil || !errors.Is(err, ErrSessionReferenceBudget) {
		t.Fatalf("message budget error = %v", err)
	}
	var budget *SessionReferenceBudgetError
	if !errors.As(err, &budget) || budget.Kind != "messages" || budget.Used != 2 {
		t.Fatalf("message budget detail = %+v", err)
	}
	if !strings.Contains(err.Error(), "messages") {
		t.Fatalf("message budget error text = %q", err.Error())
	}

	err = CheckSessionReferenceBudget(5, -3)
	if err == nil || !errors.Is(err, ErrSessionReferenceBudget) {
		t.Fatalf("rune budget error = %v", err)
	}
	if !errors.As(err, &budget) || budget.Kind != "runes" || budget.Used != 3 {
		t.Fatalf("rune budget detail = %+v", err)
	}
}
