package contracts

import "strings"

// SessionReferencePrefix marks a reference to another conversation in a prompt
// (the old shell's @session reference). The composer inserts
// "@session:<session-id>" and the agent expands referenced transcripts into the
// model input for that turn.
const SessionReferencePrefix = "@session:"

// FormatSessionReference renders the mention token for sessionID.
func FormatSessionReference(sessionID string) string {
	return SessionReferencePrefix + strings.TrimSpace(sessionID)
}

// ParseSessionReferences extracts the distinct session IDs mentioned in text,
// in first-appearance order. A reference runs from a prefix up to the first
// delimiter (whitespace, punctuation, quotes/brackets or a second prefix);
// trailing dots are trimmed so sentence punctuation does not stick to the ID,
// while inner punctuation such as "team/a.b" is preserved.
func ParseSessionReferences(text string) []string {
	seen := make(map[string]bool)
	refs := make([]string, 0)
	for _, field := range strings.Fields(text) {
		rest := field
		for {
			index := strings.Index(rest, SessionReferencePrefix)
			if index < 0 {
				break
			}
			candidate := rest[index+len(SessionReferencePrefix):]
			runes := []rune(candidate)
			limit := len(runes)
			if next := strings.Index(candidate, SessionReferencePrefix); next >= 0 {
				limit = len([]rune(candidate[:next]))
			}
			end := 0
			for end < limit && !sessionReferenceDelimiter(runes[end]) {
				end++
			}
			id := strings.TrimRight(string(runes[:end]), ".")
			if id != "" && !seen[id] {
				seen[id] = true
				refs = append(refs, id)
			}
			rest = string(runes[end:])
		}
	}
	return refs
}

// sessionReferenceDelimiter reports whether r ends a session reference.
func sessionReferenceDelimiter(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', ',', ';', ':', '!', '?', ']', '}', ')', '>',
		'"', '\'', '，', '。', '；', '：', '！', '？', '）', '」', '》':
		return true
	}
	return false
}

// Session effort settings: the per-session reasoning effort override.
func SessionEffortSettingKey(sessionID string) string {
	return "session." + sessionID + ".effort"
}

// SessionEffortLow/Medium/High are the accepted effort values.
const (
	SessionEffortLow    = "low"
	SessionEffortMedium = "medium"
	SessionEffortHigh   = "high"
)
