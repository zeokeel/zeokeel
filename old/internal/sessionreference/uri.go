package sessionreference

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// uriPayloadPattern is the base64url shape DSH requires for a canonical
// payload: at least one URL-safe base64 character and no padding.
var uriPayloadPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// jsonString renders s as the JSON string literal JavaScript's JSON.stringify
// produces for it, which is the payload DSH's canonical URI encodes. HTML
// characters stay literal (Go's encoder would escape them), so the same id
// yields the same URI as DSH.
func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// A string always encodes; the fallback keeps the URI lossless even
		// if a future encoder change makes this path reachable.
		return strconv.Quote(s)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// EncodeURI returns the canonical lossless dsh-session: URI of any session id.
func EncodeURI(sessionID string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(jsonString(sessionID)))
	return Scheme + payload
}

// DecodeURI decodes one canonical session-reference URI. Anything that is not
// exactly the canonical encoding of a string id is an invalid-reference error,
// so a malformed URI can never be silently treated as text.
func DecodeURI(uri string) (string, error) {
	if !strings.HasPrefix(uri, Scheme) {
		return "", invalidURI(uri, nil)
	}
	payload := uri[len(Scheme):]
	if !uriPayloadPattern.MatchString(payload) {
		return "", invalidURI(uri, nil)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", invalidURI(uri, err)
	}
	var sessionID string
	if err := json.Unmarshal(raw, &sessionID); err != nil {
		return "", invalidURI(uri, err)
	}
	if EncodeURI(sessionID) != uri {
		return "", invalidURI(uri, errors.New("URI is not canonical"))
	}
	return sessionID, nil
}

// FormatMention renders the Markdown mention DSH hosts insert into a draft:
// `@[label](dsh-session:<base64url(id)>)`. An empty label falls back to the id;
// backslashes and closing brackets in the label are escaped.
func FormatMention(label, sessionID string) string {
	if label == "" {
		label = sessionID
	}
	return "@[" + escapeLabel(label) + "](" + EncodeURI(sessionID) + ")"
}

func invalidURI(uri string, cause error) *Error {
	return &Error{
		Code:    CodeInvalidReference,
		Message: "invalid session reference URI " + strconv.Quote(uri),
		Err:     cause,
	}
}

// escapeLabel escapes the two characters that end or escape a Markdown label.
func escapeLabel(label string) string {
	label = strings.ReplaceAll(label, `\`, `\\`)
	return strings.ReplaceAll(label, `]`, `\]`)
}

var labelEscapePattern = regexp.MustCompile(`\\(.)`)

// unescapeLabel reverses escapeLabel (and DSH's mention label escaping): a
// backslash before any character yields that character.
func unescapeLabel(label string) string {
	return labelEscapePattern.ReplaceAllString(label, "$1")
}
