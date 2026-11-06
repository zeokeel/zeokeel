package sessionreference

import (
	"sort"
	"strings"
)

// Candidates ranks the sessions a host may suggest for a cross-session
// reference, following DSH's discovery rules: exclude nothing here (the caller
// decides about the current session), rank sessions whose working directory
// matches the calling session's first, then sessions without a directory, then
// the rest, keeping the caller's input order inside each group. The query is a
// case-insensitive substring over the id, cwd, label and display title; the
// label is the latest title falling back to the session id, and the display
// title falls back to the label.
func Candidates(targetCwd, query string, limit int, sessions []Session) ([]Candidate, error) {
	if limit <= 0 {
		return nil, &Error{
			Code:    CodeInvalidReference,
			Message: "candidate limit must be a positive integer",
		}
	}
	needle := strings.ToLower(query)
	type rankedCandidate struct {
		candidate Candidate
		rank      int
		index     int
	}
	ranked := make([]rankedCandidate, 0, len(sessions))
	for index, session := range sessions {
		if strings.TrimSpace(session.ID) == "" {
			continue
		}
		label := session.Title
		if label == "" {
			label = session.ID
		}
		displayTitle := session.DisplayTitle
		if displayTitle == "" {
			displayTitle = label
		}
		if needle != "" {
			matches := strings.Contains(strings.ToLower(session.ID), needle) ||
				(session.Cwd != "" && strings.Contains(strings.ToLower(session.Cwd), needle)) ||
				strings.Contains(strings.ToLower(label), needle) ||
				strings.Contains(strings.ToLower(displayTitle), needle)
			if !matches {
				continue
			}
		}
		candidate := Candidate{
			SessionID:    session.ID,
			Label:        label,
			DisplayTitle: displayTitle,
			Cwd:          session.Cwd,
		}
		rank := 2
		switch {
		case session.Cwd != "" && targetCwd != "" && session.Cwd == targetCwd:
			candidate.SameWorkspace = true
			rank = 0
		case session.Cwd == "":
			rank = 1
		}
		ranked = append(ranked, rankedCandidate{candidate: candidate, rank: rank, index: index})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].rank != ranked[j].rank {
			return ranked[i].rank < ranked[j].rank
		}
		return ranked[i].index < ranked[j].index
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]Candidate, len(ranked))
	for i, item := range ranked {
		out[i] = item.candidate
	}
	return out, nil
}
