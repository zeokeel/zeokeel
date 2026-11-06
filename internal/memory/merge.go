package memory

import (
	"github.com/zeokeel/zeokeel/internal/store"
)

// Decision actions returned by MergeCandidates.
const (
	// ActionInsert writes the candidate as a new memory row.
	ActionInsert = "insert"
	// ActionUpdate refreshes an existing row with the exact same content
	// (deduplication by hash): bump its timestamps/importance instead of
	// inserting a duplicate.
	ActionUpdate = "update"
	// ActionSupersede writes the candidate and marks the similar existing row
	// superseded (similarity >= SupersedeThreshold).
	ActionSupersede = "supersede"
	// ActionSkip drops the candidate for this turn (invalid, duplicate in the
	// batch, or over the per-turn limit).
	ActionSkip = "skip"
)

// SupersedeThreshold is the similarity at which a new candidate replaces an
// older memory instead of being inserted next to it.
const SupersedeThreshold = 0.8

// Decision is one planned write for a candidate. The app worker executes the
// actions against the store; ExistingID is set for update/supersede, Reason for
// skip, and Similarity for supersede.
type Decision struct {
	Action     string
	Candidate  Candidate
	ExistingID string
	Similarity float64
	Reason     string
}

// MergeCandidates plans how a batch of extracted candidates merges with the
// existing live (active or pending) memories of the same scope:
//
//   - exact content hash match -> update that row (no duplicate insert);
//   - similarity >= SupersedeThreshold to a live row -> supersede the most
//     similar row (highest similarity; active beats pending on a tie);
//   - otherwise -> insert.
//
// max caps how many candidate writes one turn may produce (non-positive uses
// MaxCandidates). Candidates beyond the cap, invalid candidates, duplicates
// inside the batch, and candidates that would reuse an already-claimed row are
// returned as skip decisions so the caller can log them. The input existing
// slice is expected to be the live rows of the candidate scopes; rows of another
// scope are never matched.
func MergeCandidates(existing []store.Memory, candidates []Candidate, max int) []Decision {
	if max <= 0 {
		max = MaxCandidates
	}
	decisions := make([]Decision, 0, len(candidates))
	accepted := 0
	claimed := make(map[string]bool)
	plannedHashes := make(map[string]bool)
	plannedContents := make([]string, 0, max)
	for _, raw := range candidates {
		if accepted >= max {
			decisions = append(decisions, Decision{
				Action:    ActionSkip,
				Candidate: raw,
				Reason:    "per-turn candidate limit reached",
			})
			continue
		}
		candidate, err := ValidateCandidate(raw)
		if err != nil {
			decisions = append(decisions, Decision{Action: ActionSkip, Candidate: raw, Reason: err.Error()})
			continue
		}
		contentHash := Hash(candidate.Content)
		if plannedHashes[contentHash] {
			decisions = append(decisions, Decision{
				Action:    ActionSkip,
				Candidate: candidate,
				Reason:    "duplicate of another candidate in this batch",
			})
			continue
		}
		if similarToPlanned(candidate.Content, plannedContents) {
			decisions = append(decisions, Decision{
				Action:    ActionSkip,
				Candidate: candidate,
				Reason:    "similar to another candidate in this batch",
			})
			continue
		}

		if match, ok := exactMatch(existing, candidate, claimed); ok {
			claimed[match.ID] = true
			plannedHashes[contentHash] = true
			plannedContents = append(plannedContents, candidate.Content)
			accepted++
			decisions = append(decisions, Decision{
				Action:     ActionUpdate,
				Candidate:  candidate,
				ExistingID: match.ID,
			})
			continue
		}
		if match, similarity, ok := similarMatch(existing, candidate, claimed); ok {
			claimed[match.ID] = true
			plannedHashes[contentHash] = true
			plannedContents = append(plannedContents, candidate.Content)
			accepted++
			decisions = append(decisions, Decision{
				Action:     ActionSupersede,
				Candidate:  candidate,
				ExistingID: match.ID,
				Similarity: similarity,
			})
			continue
		}
		plannedHashes[contentHash] = true
		plannedContents = append(plannedContents, candidate.Content)
		accepted++
		decisions = append(decisions, Decision{Action: ActionInsert, Candidate: candidate})
	}
	return decisions
}

// similarToPlanned reports whether content is close to a candidate already
// planned in this batch; inserting both would create near-duplicate rows.
func similarToPlanned(content string, planned []string) bool {
	for _, other := range planned {
		if Similarity(content, other) >= SupersedeThreshold {
			return true
		}
	}
	return false
}

// liveMemory reports whether a row still participates in deduplication: active
// and pending rows do, superseded history does not.
func liveMemory(memory store.Memory) bool {
	switch memory.Status {
	case store.MemoryStatusActive, store.MemoryStatusPending, "":
		return true
	default:
		return false
	}
}

// rowHash returns the stored content hash, falling back to hashing the content
// for legacy rows written before the column existed.
func rowHash(memory store.Memory) string {
	if hash := memory.ContentHash; hash != "" {
		return hash
	}
	return Hash(memory.Content)
}

// exactMatch returns the freshest live row with the same normalized content
// hash in the candidate's scope, not already claimed by another decision.
func exactMatch(existing []store.Memory, candidate Candidate, claimed map[string]bool) (store.Memory, bool) {
	contentHash := Hash(candidate.Content)
	var best store.Memory
	found := false
	for _, memory := range existing {
		if claimed[memory.ID] || !liveMemory(memory) || memory.Scope != candidate.Scope {
			continue
		}
		if rowHash(memory) != contentHash {
			continue
		}
		if !found || fresherRow(memory, best) {
			best = memory
			found = true
		}
	}
	return best, found
}

// similarMatch returns the most similar live row in the candidate's scope whose
// content reaches SupersedeThreshold. Exact-hash matches take precedence and
// are handled before this function is called.
func similarMatch(existing []store.Memory, candidate Candidate, claimed map[string]bool) (store.Memory, float64, bool) {
	var (
		best      store.Memory
		bestScore float64
		found     bool
	)
	for _, memory := range existing {
		if claimed[memory.ID] || !liveMemory(memory) || memory.Scope != candidate.Scope {
			continue
		}
		score := Similarity(candidate.Content, memory.Content)
		if score < SupersedeThreshold {
			continue
		}
		if !found || betterSimilar(score, memory, bestScore, best) {
			best = memory
			bestScore = score
			found = true
		}
	}
	return best, bestScore, found
}

// fresherRow reports whether a is a better exact-hash target than b: newest
// update wins, then active over pending, then stable id order.
func fresherRow(a, b store.Memory) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	if rankA, rankB := statusRank(a.Status), statusRank(b.Status); rankA != rankB {
		return rankA > rankB
	}
	return a.ID < b.ID
}

// betterSimilar reports whether (scoreA, a) beats (scoreB, b): higher similarity
// wins, then the fresher/active row.
func betterSimilar(scoreA float64, a store.Memory, scoreB float64, b store.Memory) bool {
	if scoreA != scoreB {
		return scoreA > scoreB
	}
	return fresherRow(a, b)
}

// statusRank orders live statuses so active memories win ties over pending.
func statusRank(status string) int {
	switch status {
	case store.MemoryStatusActive, "":
		return 2
	case store.MemoryStatusPending:
		return 1
	default:
		return 0
	}
}
