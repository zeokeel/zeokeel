package sessionreference_test

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
)

// TestIsContextText distinguishes an injected snapshot row from user text.
func TestIsContextText(t *testing.T) {
	contextText, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "Plan notes",
		Messages:  []sessionreference.Message{{Role: "user", Text: "hello"}},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	if !sessionreference.IsContextText(contextText) {
		t.Fatalf("rendered context was not recognized as a context row")
	}
	for _, text := range []string{
		"",
		"hello",
		sessionreference.SectionHeading,
		sessionreference.SectionHeading + "\n\nmy own notes about the heading",
		"look at " + sessionreference.SectionHeading + " inside a sentence",
		"## Referenced sessions\n\nno tags here\n",
	} {
		if sessionreference.IsContextText(text) {
			t.Errorf("IsContextText(%q) = true, want false", text)
		}
	}
}

// TestSummarizeContext covers the folded row's source count and labels.
func TestSummarizeContext(t *testing.T) {
	contextText, err := sessionreference.RenderContext([]sessionreference.Source{
		{
			SessionID: "abc",
			Label:     "Plan notes",
			Messages: []sessionreference.Message{
				{Role: "user", Text: "one"},
				{Role: "assistant", Text: "two"},
			},
		},
		{
			SessionID: "def",
			Label:     "",
			Messages:  []sessionreference.Message{{Role: "user", Text: "three"}},
		},
	})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	summary, ok := sessionreference.SummarizeContext(contextText)
	if !ok {
		t.Fatal("SummarizeContext did not recognize the context row")
	}
	if summary.Sources != 2 {
		t.Fatalf("Sources = %d, want 2", summary.Sources)
	}
	if len(summary.Labels) != 2 || summary.Labels[0] != "Plan notes" || summary.Labels[1] != "def" {
		t.Fatalf("Labels = %v, want [Plan notes def]", summary.Labels)
	}
	if len(summary.Messages) != 2 || summary.Messages[0] != 2 || summary.Messages[1] != 1 {
		t.Fatalf("Messages = %v, want [2 1]", summary.Messages)
	}

	// A context envelope with a damaged payload still reports as a context row
	// (it must stay hidden) with no sources to list.
	broken := sessionreference.SectionHeading + "\n\n" + sessionreference.Intro + "\n" +
		sessionreference.Warning + "\n\n" + sessionreference.OpenTag + "\nnot json\n" + sessionreference.CloseTag
	summary, ok = sessionreference.SummarizeContext(broken)
	if !ok || summary.Sources != 0 {
		t.Fatalf("broken context summary = %+v (ok=%v), want ok with 0 sources", summary, ok)
	}
	if _, ok := sessionreference.SummarizeContext("a normal user message"); ok {
		t.Fatal("normal user text summarized as a context row")
	}
}

// TestContextLabelFallback checks a wide label list stays a compact one-line
// summary (the shell joins it) without leaking message bodies.
func TestSummarizeContextDoesNotExposeBodies(t *testing.T) {
	contextText, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "Secret",
		Messages:  []sessionreference.Message{{Role: "user", Text: "SECRET-BODY-MARKER"}},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	summary, _ := sessionreference.SummarizeContext(contextText)
	joined := strings.Join(summary.Labels, " ")
	if strings.Contains(joined, "SECRET-BODY-MARKER") {
		t.Fatalf("summary exposed a message body: %q", joined)
	}
}

// TestSummarizeContextOmittedCounters keeps the disclosure's omission facts
// available without exposing the payload.
func TestSummarizeContextOmittedCounters(t *testing.T) {
	contextText, err := sessionreference.RenderContext([]sessionreference.Source{{
		SessionID: "abc",
		Label:     "x",
		Messages: []sessionreference.Message{
			{Role: "user", Text: "kept"},
			{Role: "assistant", Text: "nul \x00 dropped"},
		},
	}})
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	summary, ok := sessionreference.SummarizeContext(contextText)
	if !ok {
		t.Fatal("context row not recognized")
	}
	if summary.OmittedMessages != 1 || summary.OmittedBytes != len("nul \x00 dropped") {
		t.Fatalf("omitted counters = %d/%d, want 1/%d",
			summary.OmittedMessages, summary.OmittedBytes, len("nul \x00 dropped"))
	}
}
