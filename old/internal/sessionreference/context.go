package sessionreference

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// Retention constants shared by the bounded snapshot renderer. The marker is a
// fixed string so the byte accounting stays exact; the omitted byte count in
// the JSON fields reports how much original text was dropped.
const (
	truncationMarker = "\n\n…[truncated]…\n\n"
	// minRetainedBytes is the shortest text the truncation loop keeps before it
	// gives up on a message and drops it entirely.
	minRetainedBytes = 32
)

// renderedMessage is one model-visible projected message.
type renderedMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// renderedSource is the model-visible snapshot of one referenced session.
type renderedSource struct {
	SessionID       string            `json:"sessionId"`
	Label           string            `json:"label"`
	Cwd             string            `json:"cwd,omitempty"`
	Conversation    []renderedMessage `json:"conversation"`
	OmittedMessages int               `json:"omittedMessages"`
	OmittedBytes    int               `json:"omittedBytes"`
}

// RenderContext renders the second user-role message DSH puts directly after a
// message that cites other sessions: a fixed heading and untrusted-background
// warning, then one bounded JSON snapshot per source inside the
// <referenced-sessions> tags. Each source is limited to MaxReferenceBytes of
// serialized JSON by dropping its oldest messages first and shortening the
// remaining text only after that; the omitted counters preserve the exact loss
// so the model knows the preview is partial. Source text is tag-escaped, so a
// referenced session cannot spell the closing delimiter itself.
func RenderContext(sources []Source) (string, error) {
	if len(sources) == 0 {
		return "", nil
	}
	if len(sources) > MaxReferences {
		return "", &Error{
			Code:    CodeTooMany,
			Message: "a message may reference at most three sessions",
		}
	}
	rendered := make([]renderedSource, 0, len(sources))
	for _, source := range sources {
		item, err := renderSource(source)
		if err != nil {
			return "", err
		}
		rendered = append(rendered, item)
	}
	var out bytes.Buffer
	out.WriteString(SectionHeading)
	out.WriteString("\n\n")
	out.WriteString(Intro)
	out.WriteString("\n")
	out.WriteString(Warning)
	out.WriteString("\n\n")
	out.WriteString(OpenTag)
	out.WriteString("\n")
	out.WriteString(tagSafeJSON(rendered))
	out.WriteString("\n")
	out.WriteString(CloseTag)
	return out.String(), nil
}

// renderSource projects one source's text messages and fits its JSON snapshot
// into MaxReferenceBytes.
func renderSource(source Source) (renderedSource, error) {
	if source.SessionID == "" {
		return renderedSource{}, &Error{
			Code:    CodeInvalidReference,
			Message: "referenced session snapshot has no session id",
		}
	}
	item := renderedSource{
		SessionID:    source.SessionID,
		Label:        source.Label,
		Cwd:          source.Cwd,
		Conversation: make([]renderedMessage, 0, len(source.Messages)),
	}
	if item.Label == "" {
		item.Label = source.SessionID
	}
	// originals keeps each retained message's full original text so the
	// truncation loop can always re-cut from the source instead of from an
	// already-shortened preview.
	originals := make([]string, 0, len(source.Messages))
	for _, message := range source.Messages {
		// Only the projected conversation travels: user and assistant text.
		// Tool results, reasoning and other injected context never do.
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Text == "" {
			continue
		}
		if !utf8.ValidString(message.Text) || bytes.IndexByte([]byte(message.Text), 0) >= 0 {
			// Binary/NUL-bearing text is not representable context: skip it
			// and count the omission instead of corrupting the JSON.
			item.OmittedMessages++
			item.OmittedBytes += len(message.Text)
			continue
		}
		item.Conversation = append(item.Conversation, renderedMessage{
			Role: message.Role,
			Text: message.Text,
		})
		originals = append(originals, message.Text)
	}
	// Retention step one: drop the oldest messages while more than the newest
	// one remains, exactly the DSH order (older context goes before text is
	// shortened).
	for len(item.Conversation) > 1 && snapshotBytes(item) > MaxReferenceBytes {
		dropped := item.Conversation[0]
		item.Conversation = item.Conversation[1:]
		originals = originals[1:]
		item.OmittedMessages++
		item.OmittedBytes += len(dropped.Text)
	}
	// Retention step two: shorten the remaining text. The newest message is
	// kept ahead of older ones, but a single oversized message must still be
	// cut to fit the per-source byte cap.
	for snapshotBytes(item) > MaxReferenceBytes {
		longest, contentLen := longestRetained(item.Conversation)
		if longest < 0 {
			return renderedSource{}, &Error{
				Code:    CodeBudgetExceeded,
				Message: "referenced session snapshot cannot fit the byte budget",
			}
		}
		original := originals[longest]
		overflow := snapshotBytes(item) - MaxReferenceBytes
		target := contentLen - overflow - len(truncationMarker) - 8
		if target < minRetainedBytes {
			// Even a minimal preview of this message cannot fit: drop it and
			// report the whole text as omitted.
			item.Conversation = append(item.Conversation[:longest], item.Conversation[longest+1:]...)
			originals = append(originals[:longest], originals[longest+1:]...)
			item.OmittedMessages++
			item.OmittedBytes += len(original)
			continue
		}
		kept := headTail(original, target)
		item.Conversation[longest].Text = kept + truncationMarker
		if removed := contentLen - len(kept); removed > 0 {
			item.OmittedBytes += removed
		}
	}
	return item, nil
}

// longestRetained returns the index and current content byte length of the
// longest retained message text, excluding the fixed truncation marker.
func longestRetained(conversation []renderedMessage) (index, contentLen int) {
	index = -1
	for i, message := range conversation {
		length := len(message.Text)
		if length >= len(truncationMarker) && bytes.HasSuffix([]byte(message.Text), []byte(truncationMarker)) {
			length -= len(truncationMarker)
		}
		if length > contentLen {
			contentLen, index = length, i
		}
	}
	return index, contentLen
}

// headTail returns at most target bytes of text split between its head and
// tail, never cutting a UTF-8 sequence in half.
func headTail(text string, target int) string {
	if target >= len(text) {
		return text
	}
	if target <= 0 {
		return ""
	}
	head := target / 2
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	tailStart := len(text) - (target - head)
	for tailStart < len(text) && !utf8.RuneStart(text[tailStart]) {
		tailStart++
	}
	if tailStart < head {
		tailStart = head
	}
	return text[:head] + text[tailStart:]
}

// snapshotBytes is the serialized size of one source's JSON snapshot.
func snapshotBytes(item renderedSource) int {
	return len(tagSafeJSON(item))
}

// tagSafeJSON serializes v without Go's HTML escaping (so the JSON matches
// JavaScript's JSON.stringify) and then escapes every '<' as the JSON escape
// \u003c. Source data therefore cannot spell the <referenced-sessions> tags
// while decoding to the exact original text.
func tagSafeJSON(value any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "[]"
	}
	serialized := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return string(bytes.ReplaceAll(serialized, []byte("<"), []byte(`\u003c`)))
}
