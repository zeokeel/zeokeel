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
// in first-appearance order. Tokens are whitespace-delimited; a trailing
// punctuation character is trimmed.
func ParseSessionReferences(text string) []string {
	seen := make(map[string]bool)
	refs := make([]string, 0)
	for _, field := range strings.Fields(text) {
		index := strings.Index(field, SessionReferencePrefix)
		if index < 0 {
			continue
		}
		id := strings.TrimSpace(field[index+len(SessionReferencePrefix):])
		id = strings.TrimRight(id, ".,;:!?，。；：！？)")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, id)
	}
	return refs
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
