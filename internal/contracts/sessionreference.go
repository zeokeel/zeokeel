package contracts

// Session-reference candidates, URI codec and budget errors: shared helpers for
// the composer's @session menu and the agent's reference expansion (the old
// shell's sessionreference package).

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// DefaultSessionReferenceLimit is the candidate-list cap when limit <= 0.
const DefaultSessionReferenceLimit = 8

// SessionReferenceCandidate is one conversation the @ menu can reference.
type SessionReferenceCandidate struct {
	SessionID string
	Title     string
	// Score is an optional backend-provided relevance hint (higher is better);
	// it breaks ties inside one match class.
	Score     int
	UpdatedAt time.Time
}

// referenceMatchRank ranks one candidate against the normalized query:
// 0 = exact session ID, 1 = session ID prefix, 2 = title contains,
// -1 = no match.
func referenceMatchRank(candidate SessionReferenceCandidate, query string) int {
	id := strings.ToLower(strings.TrimSpace(candidate.SessionID))
	title := strings.ToLower(candidate.Title)
	switch {
	case id == query:
		return 0
	case strings.HasPrefix(id, query):
		return 1
	case strings.Contains(title, query):
		return 2
	default:
		return -1
	}
}

// betterCandidate reports whether a should replace b for the same session ID
// (the candidate table may carry duplicates from several sources).
func betterCandidate(a, b SessionReferenceCandidate) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.UpdatedAt.After(b.UpdatedAt)
}

// RankSessionReferenceCandidates ranks the @-menu candidates: exact session-ID
// matches first, then ID-prefix matches, then title-contains matches (query is
// case-insensitive), each class ordered by Score desc, then UpdatedAt desc, then
// session ID. With an empty query every candidate is kept, newest first.
// Duplicate session IDs collapse to their best entry; the result is capped at
// limit (limit <= 0 uses DefaultSessionReferenceLimit).
func RankSessionReferenceCandidates(candidates []SessionReferenceCandidate, query string, limit int) []SessionReferenceCandidate {
	if limit <= 0 {
		limit = DefaultSessionReferenceLimit
	}
	needle := strings.ToLower(strings.TrimSpace(query))

	best := make(map[string]SessionReferenceCandidate, len(candidates))
	for _, candidate := range candidates {
		candidate.SessionID = strings.TrimSpace(candidate.SessionID)
		if candidate.SessionID == "" {
			continue
		}
		if existing, ok := best[candidate.SessionID]; ok {
			if betterCandidate(candidate, existing) {
				best[candidate.SessionID] = candidate
			}
			continue
		}
		best[candidate.SessionID] = candidate
	}

	ranked := make([]SessionReferenceCandidate, 0, len(best))
	for _, candidate := range best {
		if needle != "" && referenceMatchRank(candidate, needle) < 0 {
			continue
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if needle == "" {
			// Empty query: newest first (the score hint only breaks ties).
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
			if left.Score != right.Score {
				return left.Score > right.Score
			}
			return left.SessionID < right.SessionID
		}
		leftRank, rightRank := referenceMatchRank(left, needle), referenceMatchRank(right, needle)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		return left.SessionID < right.SessionID
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// SessionReferenceURIScheme prefixes an encoded session reference URI.
const SessionReferenceURIScheme = "sessionref:"

// validateSessionReferenceID rejects empty IDs and IDs with whitespace or
// control characters (path punctuation is allowed and percent-encoded).
func validateSessionReferenceID(id string) error {
	if id == "" {
		return errors.New("sessionref: empty session id")
	}
	for _, r := range id {
		if unicode.IsSpace(r) || r < 0x20 || r == 0x7f {
			return fmt.Errorf("sessionref: invalid character %q in session id", r)
		}
	}
	return nil
}

// EncodeSessionReferenceURI renders one session ID as a sessionref: URI
// (path-escaped so punctuation such as "/" survives the round trip).
func EncodeSessionReferenceURI(sessionID string) (string, error) {
	id := strings.TrimSpace(sessionID)
	if err := validateSessionReferenceID(id); err != nil {
		return "", err
	}
	return SessionReferenceURIScheme + url.PathEscape(id), nil
}

// DecodeSessionReferenceURI parses a sessionref: URI back into its session ID.
// A bare session ID (no scheme) is accepted for compatibility with older
// callers.
func DecodeSessionReferenceURI(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("sessionref: empty URI")
	}
	encoded := raw
	if strings.HasPrefix(raw, SessionReferenceURIScheme) {
		encoded = raw[len(SessionReferenceURIScheme):]
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil {
		return "", fmt.Errorf("sessionref: decode %q: %w", raw, err)
	}
	id := strings.TrimSpace(decoded)
	if err := validateSessionReferenceID(id); err != nil {
		return "", err
	}
	return id, nil
}

// ErrSessionReferenceBudget marks a session-reference budget overrun.
var ErrSessionReferenceBudget = errors.New("session reference budget exceeded")

// SessionReferenceBudgetError describes one overrun: Kind is "messages" (the
// per-session message cap) or "runes" (the injected-context rune cap) and Used
// is the overrun amount.
type SessionReferenceBudgetError struct {
	Kind  string
	Limit int
	Used  int
}

// Error implements error.
func (e *SessionReferenceBudgetError) Error() string {
	return fmt.Sprintf("session reference budget exceeded: %s used %d over limit %d", e.Kind, e.Used, e.Limit)
}

// Unwrap exposes ErrSessionReferenceBudget for errors.Is.
func (e *SessionReferenceBudgetError) Unwrap() error { return ErrSessionReferenceBudget }

// CheckSessionReferenceBudget reports a negative remaining budget as a typed
// budget error: remainingMessages is checked before remainingRunes.
func CheckSessionReferenceBudget(remainingMessages, remainingRunes int) error {
	if remainingMessages < 0 {
		return &SessionReferenceBudgetError{Kind: "messages", Limit: 0, Used: -remainingMessages}
	}
	if remainingRunes < 0 {
		return &SessionReferenceBudgetError{Kind: "runes", Limit: 0, Used: -remainingRunes}
	}
	return nil
}
