package sessionreference_test

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/sessionreference"
)

// TestEncodeURIVectors pins the canonical byte form: the payload is the
// base64url encoding of the JSON string of the id, exactly like DSH's
// encodeSessionReferenceUri.
func TestEncodeURIVectors(t *testing.T) {
	cases := []struct{ id, want string }{
		{"abc", "dsh-session:ImFiYyI"},
		{"会话 一", "dsh-session:IuS8muivnSDkuIAi"},
		{"quote\"and\\backslash", "dsh-session:InF1b3RlXCJhbmRcXGJhY2tzbGFzaCI"},
		{"a]b", "dsh-session:ImFdYiI"},
		{"<tag>", "dsh-session:Ijx0YWc-Ig"},
		{"emoji🙂", "dsh-session:ImVtb2pp8J-ZgiI"},
		{"", "dsh-session:IiI"},
	}
	for _, tc := range cases {
		if got := sessionreference.EncodeURI(tc.id); got != tc.want {
			t.Errorf("EncodeURI(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestDecodeURIExactRoundTrip proves every id round-trips exactly, including
// Unicode labels and empty ids.
func TestDecodeURIExactRoundTrip(t *testing.T) {
	for _, id := range []string{"abc", "会话 一", "a b c", "a]b\\c", "", "<tag>", "emoji🙂", "line\nbreak"} {
		uri := sessionreference.EncodeURI(id)
		got, err := sessionreference.DecodeURI(uri)
		if err != nil {
			t.Fatalf("DecodeURI(EncodeURI(%q)) error = %v", id, err)
		}
		if got != id {
			t.Fatalf("DecodeURI(EncodeURI(%q)) = %q, want exact round trip", id, got)
		}
	}
}

// TestDecodeURIRejectsMalformed pins the explicit errors: wrong scheme, empty
// or non-base64url payloads, non-string JSON and non-canonical encodings all
// fail as invalid references instead of being treated as text.
func TestDecodeURIRejectsMalformed(t *testing.T) {
	for _, uri := range []string{
		"",
		"abc",
		"http://example.com",
		"dsh-session:",
		"dsh-session:bad!",
		"dsh-session:AAAA",
		"dsh-session:MQ",             // base64url of "1": valid JSON, not a string
		"dsh-session:Ilx1MDA2MWJjIg", // decodes to "abc" but is not canonical
		"dsh-session:ImFiYyI=",       // padding is not base64url
	} {
		if got, err := sessionreference.DecodeURI(uri); err == nil {
			t.Errorf("DecodeURI(%q) = %q, want an error", uri, got)
			continue
		} else {
			var refErr *sessionreference.Error
			if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeInvalidReference {
				t.Errorf("DecodeURI(%q) error = %v, want code %s", uri, err, sessionreference.CodeInvalidReference)
			}
		}
	}
}

// TestFormatMention covers the readable label, the id fallback and label
// escaping for the Markdown form.
func TestFormatMention(t *testing.T) {
	cases := []struct{ label, id, want string }{
		{"Plan notes", "abc", `@[Plan notes](dsh-session:ImFiYyI)`},
		{"会话 一", "abc", `@[会话 一](dsh-session:ImFiYyI)`},
		{"", "abc", `@[abc](dsh-session:ImFiYyI)`},
		{`a]b\c`, "abc", `@[a\]b\\c](dsh-session:ImFiYyI)`},
	}
	for _, tc := range cases {
		if got := sessionreference.FormatMention(tc.label, tc.id); got != tc.want {
			t.Errorf("FormatMention(%q, %q) = %q, want %q", tc.label, tc.id, got, tc.want)
		}
	}
}

// TestParseText covers Markdown and bare canonical references, order, label
// unescaping and the distinction between plain scheme chatter and malformed
// references.
func TestParseText(t *testing.T) {
	const abcURI = "dsh-session:ImFiYyI"
	cases := []struct {
		name      string
		text      string
		wantText  string
		wantRefs  []sessionreference.Reference
		wantError bool
	}{
		{
			name:     "no reference",
			text:     "hello @world, dsh-session: is a scheme",
			wantText: "hello @world, dsh-session: is a scheme",
		},
		{
			name:     "markdown mention",
			text:     "compare with @[Plan notes](" + abcURI + ") first",
			wantText: "compare with @Plan notes first",
			wantRefs: []sessionreference.Reference{{SessionID: "abc", Label: "Plan notes"}},
		},
		{
			name:     "chinese label with spaces",
			text:     "看 @[会话 一](" + abcURI + ")",
			wantText: "看 @会话 一",
			wantRefs: []sessionreference.Reference{{SessionID: "abc", Label: "会话 一"}},
		},
		{
			name:     "bare uri",
			text:     "see " + abcURI + " today",
			wantText: "see @abc today",
			wantRefs: []sessionreference.Reference{{SessionID: "abc", Label: "abc"}},
		},
		{
			name:     "bare uri followed by punctuation",
			text:     abcURI + ".",
			wantText: "@abc.",
			wantRefs: []sessionreference.Reference{{SessionID: "abc", Label: "abc"}},
		},
		{
			name:     "escaped label",
			text:     `@[a\]b\\c](` + abcURI + `)`,
			wantText: `@a]b\c`,
			wantRefs: []sessionreference.Reference{{SessionID: "abc", Label: `a]b\c`}},
		},
		{
			name:     "references keep appearance order",
			text:     "a " + abcURI + " b @[Second](" + sessionreference.EncodeURI("id2") + ")",
			wantText: "a @abc b @Second",
			wantRefs: []sessionreference.Reference{
				{SessionID: "abc", Label: "abc"},
				{SessionID: "id2", Label: "Second"},
			},
		},
		{
			name:     "non dsh markdown link is plain text",
			text:     "@[x](https://example.com)",
			wantText: "@[x](https://example.com)",
		},
		{
			name:     "punctuation only scheme mention is plain text",
			text:     "dsh-session: !!!",
			wantText: "dsh-session: !!!",
		},
		{
			name:      "malformed explicit mention",
			text:      "@[x](dsh-session:bad!)",
			wantError: true,
		},
		{
			name:      "explicit mention with empty payload",
			text:      "@[x](dsh-session:)",
			wantError: true,
		},
		{
			name:      "malformed bare uri",
			text:      "see dsh-session:AAAA",
			wantError: true,
		},
		{
			name:      "non-canonical bare uri",
			text:      "see dsh-session:Ilx1MDA2MWJjIg",
			wantError: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, refs, err := sessionreference.ParseText(tc.text)
			if tc.wantError {
				if err == nil {
					t.Fatalf("ParseText(%q) error = nil, want an explicit error", tc.text)
				}
				var refErr *sessionreference.Error
				if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeInvalidReference {
					t.Fatalf("ParseText(%q) error = %v, want code %s", tc.text, err, sessionreference.CodeInvalidReference)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseText(%q) error = %v", tc.text, err)
			}
			if text != tc.wantText {
				t.Errorf("ParseText(%q) text = %q, want %q", tc.text, text, tc.wantText)
			}
			if len(refs) != len(tc.wantRefs) {
				t.Fatalf("ParseText(%q) refs = %+v, want %+v", tc.text, refs, tc.wantRefs)
			}
			for i := range refs {
				if refs[i] != tc.wantRefs[i] {
					t.Errorf("ParseText(%q) refs[%d] = %+v, want %+v", tc.text, i, refs[i], tc.wantRefs[i])
				}
			}
		})
	}
}

// TestParseTextRoundTrip proves the mention formatter and parser are inverse.
func TestParseTextRoundTrip(t *testing.T) {
	mention := sessionreference.FormatMention("会话 一", "session-中文-42")
	text, refs, err := sessionreference.ParseText("open " + mention + " please")
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if text != "open @会话 一 please" {
		t.Fatalf("round-trip text = %q", text)
	}
	if len(refs) != 1 || refs[0].SessionID != "session-中文-42" || refs[0].Label != "会话 一" {
		t.Fatalf("round-trip refs = %+v", refs)
	}
}

// TestRenderReadable is the transcript's permissive rendering: valid references
// become @label, malformed ones stay as typed instead of failing the frame.
func TestRenderReadable(t *testing.T) {
	cases := []struct{ text, want string }{
		{"plain text", "plain text"},
		{"look " + sessionreference.FormatMention("Plan", "abc"), "look @Plan"},
		{"bad @[x](dsh-session:bad!) mention", "bad @[x](dsh-session:bad!) mention"},
		{"mixed " + sessionreference.FormatMention("ok", "abc") + " @[x](dsh-session:bad!)", "mixed @ok @[x](dsh-session:bad!)"},
	}
	for _, tc := range cases {
		if got := sessionreference.RenderReadable(tc.text); got != tc.want {
			t.Errorf("RenderReadable(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// TestNormalize covers the reference list hygiene DSH applies before preparing
// context: first-appearance order, de-duplication, self-reference rejection and
// the fixed maximum.
func TestNormalize(t *testing.T) {
	refs := []sessionreference.Reference{
		{SessionID: "a", Label: "First"},
		{SessionID: "b", Label: ""},
		{SessionID: "a", Label: "Second"},
		{SessionID: "c", Label: "Third"},
	}
	got, err := sessionreference.Normalize(refs, "target")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	want := []sessionreference.Reference{
		{SessionID: "a", Label: "First"},
		{SessionID: "b", Label: "b"},
		{SessionID: "c", Label: "Third"},
	}
	if len(got) != len(want) {
		t.Fatalf("Normalize = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Normalize = %+v, want %+v", got, want)
		}
	}

	if _, err := sessionreference.Normalize([]sessionreference.Reference{{SessionID: "target"}}, "target"); err == nil {
		t.Fatal("self reference was accepted")
	} else {
		var refErr *sessionreference.Error
		if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeSelfReference {
			t.Fatalf("self reference error = %v, want code %s", err, sessionreference.CodeSelfReference)
		}
	}

	tooMany := []sessionreference.Reference{
		{SessionID: "a"}, {SessionID: "b"}, {SessionID: "c"}, {SessionID: "d"},
	}
	if _, err := sessionreference.Normalize(tooMany, "target"); err == nil {
		t.Fatal("four references were accepted")
	} else {
		var refErr *sessionreference.Error
		if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeTooMany {
			t.Fatalf("too many references error = %v, want code %s", err, sessionreference.CodeTooMany)
		}
	}

	if _, err := sessionreference.Normalize([]sessionreference.Reference{{Label: "no id"}}, "target"); err == nil {
		t.Fatal("reference without a session id was accepted")
	} else {
		var refErr *sessionreference.Error
		if !errors.As(err, &refErr) || refErr.Code != sessionreference.CodeInvalidReference {
			t.Fatalf("empty id error = %v, want code %s", err, sessionreference.CodeInvalidReference)
		}
	}

	if got, err := sessionreference.Normalize(nil, "target"); err != nil || len(got) != 0 {
		t.Fatalf("Normalize(nil) = %+v, %v", got, err)
	}
}
