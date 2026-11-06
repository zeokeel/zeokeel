package sessionreference

import (
	"encoding/json"
	"strings"
)

// ContextSummary is the compact description of one injected referenced-session
// context message: how many source snapshots it carries and their labels. It
// never exposes the raw JSON, which is what lets a transcript fold the
// injected row instead of printing it as a user message.
type ContextSummary struct {
	Sources int
	// Labels is one display label per source (the reference label, falling
	// back to the session id).
	Labels []string
	// Messages is the retained projected message count per source.
	Messages []int
	// OmittedMessages and OmittedBytes aggregate the per-source omission
	// counters the payload carries.
	OmittedMessages int
	OmittedBytes    int
}

// IsContextText reports whether text is an injected referenced-session context
// row rather than a message the user wrote. The check is the stable envelope
// the controller persists (fixed heading, intro, warning and tag pair), not
// just the heading, so a user message that merely starts with the heading is
// still a user message.
func IsContextText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, SectionHeading) ||
		!strings.Contains(trimmed, Intro) ||
		!strings.Contains(trimmed, Warning) {
		return false
	}
	open := strings.Index(trimmed, OpenTag)
	close := strings.LastIndex(trimmed, CloseTag)
	return open >= 0 && close > open
}

// SummarizeContext parses an injected context row for the folded transcript
// row. ok is false when text is not a context row. A context row whose payload
// cannot be parsed still reports ok=true with zero sources: the raw row must
// stay hidden either way.
func SummarizeContext(text string) (ContextSummary, bool) {
	if !IsContextText(text) {
		return ContextSummary{}, false
	}
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, OpenTag)
	end := strings.LastIndex(trimmed, CloseTag)
	payload := strings.TrimSpace(trimmed[start+len(OpenTag) : end])
	var sources []struct {
		SessionID       string            `json:"sessionId"`
		Label           string            `json:"label"`
		Conversation    []json.RawMessage `json:"conversation"`
		OmittedMessages int               `json:"omittedMessages"`
		OmittedBytes    int               `json:"omittedBytes"`
	}
	if err := json.Unmarshal([]byte(payload), &sources); err != nil {
		// The envelope is ours, the payload is damaged: it still must not
		// render as a user message, so report a context row with no sources.
		return ContextSummary{}, true
	}
	summary := ContextSummary{
		Sources:  len(sources),
		Labels:   make([]string, len(sources)),
		Messages: make([]int, len(sources)),
	}
	for i, source := range sources {
		label := source.Label
		if label == "" {
			label = source.SessionID
		}
		summary.Labels[i] = label
		summary.Messages[i] = len(source.Conversation)
		summary.OmittedMessages += source.OmittedMessages
		summary.OmittedBytes += source.OmittedBytes
	}
	return summary, true
}
