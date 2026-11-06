package sessionreference_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
)

// renderedSource is the JSON shape one source occupies inside the
// <referenced-sessions> payload.
type renderedSource struct {
	SessionID    string `json:"sessionId"`
	Label        string `json:"label"`
	Cwd          string `json:"cwd"`
	Conversation []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"conversation"`
	OmittedMessages int `json:"omittedMessages"`
	OmittedBytes    int `json:"omittedBytes"`
}

// promptPayload returns the JSON array between the referenced-sessions tags.
func promptPayload(t *testing.T, prompt string) []json.RawMessage {
	t.Helper()
	open := sessionreference.OpenTag + "\n"
	close := "\n" + sessionreference.CloseTag
	start := strings.Index(prompt, open)
	end := strings.LastIndex(prompt, close)
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("prompt has no referenced-sessions payload: %q", prompt)
	}
	raw := prompt[start+len(open) : end]
	var sources []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		t.Fatalf("payload is not a JSON array: %v\n%s", err, raw)
	}
	return sources
}

func decodeRendered(t *testing.T, raw json.RawMessage) renderedSource {
	t.Helper()
	var source renderedSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatalf("payload source is not valid JSON: %v\n%s", err, raw)
	}
	return source
}

// TestRenderContextShape covers the fixed model-visible envelope: heading,
// warning, tags and one JSON object per source in input order.
func TestRenderContextShape(t *testing.T) {
	prompt, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "Plan notes",
		Cwd:       "/work",
		Messages: []sessionreference.Message{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "hi there"},
		},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, want := range []string{
		sessionreference.SectionHeading,
		sessionreference.Intro,
		sessionreference.Warning,
		sessionreference.OpenTag,
		sessionreference.CloseTag,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	sources := promptPayload(t, prompt)
	if len(sources) != 1 {
		t.Fatalf("payload sources = %d, want 1", len(sources))
	}
	source := decodeRendered(t, sources[0])
	if source.SessionID != "abc" || source.Label != "Plan notes" || source.Cwd != "/work" {
		t.Fatalf("rendered source = %+v", source)
	}
	if len(source.Conversation) != 2 ||
		source.Conversation[0].Role != "user" || source.Conversation[0].Text != "hello" ||
		source.Conversation[1].Role != "assistant" || source.Conversation[1].Text != "hi there" {
		t.Fatalf("rendered conversation = %+v", source.Conversation)
	}
	if source.OmittedMessages != 0 || source.OmittedBytes != 0 {
		t.Fatalf("intact source omitted counters = %d/%d, want 0/0",
			source.OmittedMessages, source.OmittedBytes)
	}
}

// TestRenderContextEscapesTagBreakouts proves source text cannot spell the
// closing delimiter: every '<' in the JSON payload is escaped, so the only
// literal closing tag is our own.
func TestRenderContextEscapesTagBreakouts(t *testing.T) {
	const broken = "x " + sessionreference.CloseTag + " y"
	prompt, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "evil",
		Messages:  []sessionreference.Message{{Role: "user", Text: broken}},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	if got := strings.Count(prompt, sessionreference.CloseTag); got != 1 {
		t.Fatalf("closing tag appears %d times, want only the delimiter:\n%s", got, prompt)
	}
	if !strings.Contains(prompt, `\u003c/referenced-sessions>`) {
		t.Fatalf("source '<' was not JSON-escaped:\n%s", prompt)
	}
}

// TestRenderContextBinaryAndNULSkipped proves non-text projected messages are
// dropped and counted, never serialized.
func TestRenderContextBinaryAndNULSkipped(t *testing.T) {
	prompt, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "notes",
		Messages: []sessionreference.Message{
			{Role: "user", Text: "keep me"},
			{Role: "assistant", Text: "nul \x00 inside"},
			{Role: "tool", Text: "tool output must not travel"},
			{Role: "user", Text: "bad \xff utf8"},
			{Role: "user", Text: ""},
		},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	source := decodeRendered(t, promptPayload(t, prompt)[0])
	if len(source.Conversation) != 1 || source.Conversation[0].Text != "keep me" {
		t.Fatalf("rendered conversation = %+v, want only the text message", source.Conversation)
	}
	if source.OmittedMessages != 2 {
		t.Fatalf("omittedMessages = %d, want 2 (NUL + invalid UTF-8)", source.OmittedMessages)
	}
	if source.OmittedBytes != len("nul \x00 inside")+len("bad \xff utf8") {
		t.Fatalf("omittedBytes = %d, want the skipped text bytes", source.OmittedBytes)
	}
}

// TestRenderContextDropsOldestMessages first covers DSH's retention order:
// drop the oldest non-newest messages before shortening any text, and keep the
// newest message intact while it fits.
func TestRenderContextDropsOldestMessages(t *testing.T) {
	var messages []sessionreference.Message
	for i := 0; i < 20; i++ {
		messages = append(messages, sessionreference.Message{
			Role: "user",
			Text: strings.Repeat("x", 1024) + " message" + string(rune('a'+i)),
		})
	}
	prompt, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "long",
		Messages:  messages,
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	raw := promptPayload(t, prompt)[0]
	if len(raw) > sessionreference.MaxReferenceBytes {
		t.Fatalf("source JSON = %d bytes, cap is %d", len(raw), sessionreference.MaxReferenceBytes)
	}
	source := decodeRendered(t, raw)
	if len(source.Conversation) == 0 {
		t.Fatalf("retention dropped every message: %+v", source)
	}
	last := messages[len(messages)-1].Text
	if got := source.Conversation[len(source.Conversation)-1].Text; got != last {
		t.Fatalf("newest message was not kept intact: got %q", got)
	}
	if source.OmittedMessages == 0 {
		t.Fatalf("omittedMessages = 0, want the dropped older messages counted")
	}
}

// TestRenderContextTruncatesOneHugeMessage covers the second retention step:
// when a single (newest) message exceeds the budget its text is shortened, the
// result still fits the byte cap, stays valid UTF-8, and reports the omitted
// bytes.
func TestRenderContextTruncatesOneHugeMessage(t *testing.T) {
	huge := strings.Repeat("长文本", 8000) // 24000 bytes of valid UTF-8
	prompt, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "huge",
		Messages:  []sessionreference.Message{{Role: "user", Text: huge}},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	raw := promptPayload(t, prompt)[0]
	if len(raw) > sessionreference.MaxReferenceBytes {
		t.Fatalf("source JSON = %d bytes, cap is %d", len(raw), sessionreference.MaxReferenceBytes)
	}
	source := decodeRendered(t, raw)
	if len(source.Conversation) != 1 {
		t.Fatalf("conversation = %+v, want the single retained message", source.Conversation)
	}
	text := source.Conversation[0].Text
	if !utf8.ValidString(text) {
		t.Fatalf("retained text is not valid UTF-8")
	}
	if !strings.Contains(text, "truncated") {
		t.Fatalf("retained text is missing the truncation notice: %q", text[:min(120, len(text))])
	}
	if source.OmittedMessages != 0 {
		t.Fatalf("omittedMessages = %d, want 0 (only text was shortened)", source.OmittedMessages)
	}
	if source.OmittedBytes <= 0 || source.OmittedBytes >= len(huge) {
		t.Fatalf("omittedBytes = %d, want a partial byte count of %d", source.OmittedBytes, len(huge))
	}
}

// TestRenderContextPerSourceBudget proves the cap applies to each source
// separately, not to the aggregate payload.
func TestRenderContextPerSourceBudget(t *testing.T) {
	big := strings.Repeat("a", 6000)
	sources := []sessionreference.Source{
		{SessionID: "one", Label: "one", Messages: []sessionreference.Message{{Role: "user", Text: big}}},
		{SessionID: "two", Label: "two", Messages: []sessionreference.Message{{Role: "user", Text: big}}},
		{SessionID: "three", Label: "three", Messages: []sessionreference.Message{{Role: "user", Text: big}}},
	}
	prompt, err := sessionreference.RenderContext(sources)
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	raws := promptPayload(t, prompt)
	if len(raws) != 3 {
		t.Fatalf("payload sources = %d, want 3", len(raws))
	}
	for i, raw := range raws {
		if len(raw) > sessionreference.MaxReferenceBytes {
			t.Errorf("source %d JSON = %d bytes, cap is %d", i, len(raw), sessionreference.MaxReferenceBytes)
		}
	}
}

// TestRenderContextValidation covers the fixed limits and the empty case.
func TestRenderContextValidation(t *testing.T) {
	if prompt, err := sessionreference.RenderContext(nil); err != nil || prompt != "" {
		t.Fatalf("RenderContext(nil) = %q, %v; want empty, nil", prompt, err)
	}

	tooMany := make([]sessionreference.Source, sessionreference.MaxReferences+1)
	for i := range tooMany {
		tooMany[i] = sessionreference.Source{SessionID: string(rune('a' + i)), Label: "x"}
	}
	if _, err := sessionreference.RenderContext(tooMany); err == nil {
		t.Fatal("more than MaxReferences sources were accepted")
	} else {
		var refErr *sessionreference.Error
		if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeTooMany {
			t.Fatalf("too many sources error = %v, want code %s", err, sessionreference.CodeTooMany)
		}
	}

	if _, err := sessionreference.RenderContext([]sessionreference.Source{{Label: "no id"}}); err == nil {
		t.Fatal("source without a session id was accepted")
	} else {
		var refErr *sessionreference.Error
		if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeInvalidReference {
			t.Fatalf("empty source id error = %v, want code %s", err, sessionreference.CodeInvalidReference)
		}
	}
}
