package runtime

import "testing"

// TestAttachmentPromptEscapesName pins the attachment header syntax for file
// names containing the XML metacharacters that would otherwise terminate the
// quoted name attribute. The attachment body must stay verbatim.
func TestAttachmentPromptEscapesName(t *testing.T) {
	const body = `body <tag> & "quoted"`

	cases := []struct {
		name   string
		file   string
		header string
	}{
		{"plain", "notes.txt", `<attachment name="notes.txt">`},
		{"quote", `q"uote.txt`, `<attachment name="q&quot;uote.txt">`},
		{"angle brackets", "<notes>.txt", `<attachment name="&lt;notes&gt;.txt">`},
		{"ampersand", "a&b.txt", `<attachment name="a&amp;b.txt">`},
		{"all metacharacters", `q"uote<&>.txt`, `<attachment name="q&quot;uote&lt;&amp;&gt;.txt">`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AttachmentPrompt("see file", []UserAttachment{{Name: tc.file, Text: body}})
			want := "see file\n\n" + tc.header + "\n" + body + "\n</attachment>"
			if got != want {
				t.Fatalf("AttachmentPrompt() = %q, want %q", got, want)
			}
		})
	}
}
