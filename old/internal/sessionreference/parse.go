package sessionreference

import (
	"regexp"
	"strings"
)

// referencePattern recognizes the two spellings of a canonical session
// reference, mirroring DSH's parser: an explicit Markdown mention
// `@[label](dsh-session:<payload>)`, or a bare `dsh-session:<payload>` token.
// Go's RE2 has no non-capturing groups, so the label's inner alternative is a
// plain capture; the groups used by the parser are label, Markdown URI and bare
// URI.
var referencePattern = regexp.MustCompile(`@\[((\\.|[^\\\]])*)\]\((dsh-session:[^\s)]*)\)|(dsh-session:[A-Za-z0-9_-]+)`)

// ParseText replaces every canonical session reference in text with the
// readable `@label` form and returns the structured references in
// first-appearance order (duplicates included; Normalize dedupes them).
// Malformed explicit Markdown mentions and malformed bare URIs are explicit
// errors, as in DSH: only scheme chatter with no base64url-shaped payload stays
// ordinary text.
func ParseText(text string) (string, []Reference, error) {
	matches := referencePattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil, nil
	}
	var out strings.Builder
	references := make([]Reference, 0, len(matches))
	last := 0
	for _, match := range matches {
		out.WriteString(text[last:match[0]])
		if match[6] >= 0 {
			label := unescapeLabel(submatch(text, match, 1))
			sessionID, err := DecodeURI(submatch(text, match, 3))
			if err != nil {
				return "", nil, err
			}
			references = append(references, Reference{SessionID: sessionID, Label: label})
			out.WriteString("@" + label)
		} else {
			uri := submatch(text, match, 4)
			sessionID, err := DecodeURI(uri)
			if err != nil {
				return "", nil, err
			}
			references = append(references, Reference{SessionID: sessionID, Label: sessionID})
			out.WriteString("@" + sessionID)
		}
		last = match[1]
	}
	out.WriteString(text[last:])
	return out.String(), references, nil
}

// RenderReadable is the transcript's best-effort rendering: canonical mentions
// become their readable @label while malformed references stay exactly as the
// user typed them. Rendering a transcript must never fail, so this never
// returns an error; model-side parsing uses the strict ParseText instead.
func RenderReadable(text string) string {
	matches := referencePattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	for _, match := range matches {
		out.WriteString(text[last:match[0]])
		replaced := false
		if markdown := submatch(text, match, 3); match[6] >= 0 {
			if _, err := DecodeURI(markdown); err == nil {
				out.WriteString("@" + unescapeLabel(submatch(text, match, 1)))
				replaced = true
			}
		} else if uri := submatch(text, match, 4); match[8] >= 0 {
			if sessionID, err := DecodeURI(uri); err == nil {
				out.WriteString("@" + sessionID)
				replaced = true
			}
		}
		if !replaced {
			out.WriteString(text[match[0]:match[1]])
		}
		last = match[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

// Normalize applies the reference-list hygiene DSH runs before preparing
// context: drop empty ids, reject a self-reference, keep first-appearance
// order, dedupe by session id and enforce MaxReferences. A reference without a
// label is labeled by its session id.
func Normalize(references []Reference, targetSessionID string) ([]Reference, error) {
	out := make([]Reference, 0, len(references))
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if strings.TrimSpace(reference.SessionID) == "" {
			return nil, &Error{Code: CodeInvalidReference, Message: "session reference has no session id"}
		}
		if reference.SessionID == targetSessionID {
			return nil, &Error{
				Code:    CodeSelfReference,
				Message: "a session cannot reference itself: " + reference.SessionID,
			}
		}
		if _, ok := seen[reference.SessionID]; ok {
			continue
		}
		seen[reference.SessionID] = struct{}{}
		label := reference.Label
		if label == "" {
			label = reference.SessionID
		}
		out = append(out, Reference{SessionID: reference.SessionID, Label: label})
	}
	if len(out) > MaxReferences {
		return nil, &Error{
			Code:    CodeTooMany,
			Message: "a message may reference at most three sessions",
		}
	}
	return out, nil
}

// submatch returns the text of group i of one FindAllStringSubmatchIndex match,
// or "" when the group did not participate.
func submatch(text string, match []int, group int) string {
	start, end := match[2*group], match[2*group+1]
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return text[start:end]
}
