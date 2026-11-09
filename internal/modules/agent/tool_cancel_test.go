package agent

// Tests for the tool-cancel classification: a user stop surfaces as an error
// tool result whose text says the context was cancelled. That result is a
// neutral cancellation, not a tool failure, and must not leak the raw English
// text into the transcript summary.

import (
	"strings"
	"testing"
)

func TestToolResultCancelled(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"context canceled", "rig: tool \"bash\": context canceled", true},
		{"context cancelled", "context cancelled", true},
		{"operation was cancelled", "operation was cancelled by user", true},
		{"cancelled while waiting", "terminal_send: cancelled while waiting for output", true},
		{"real failure", "sandbox: denied: command \"exit\" is not in the allowlist", false},
		{"timeout", "context deadline exceeded", false},
		{"empty", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := toolResultCancelled(test.content); got != test.want {
				t.Fatalf("toolResultCancelled(%q) = %v, want %v", test.content, got, test.want)
			}
		})
	}
}

// TestSummarizeToolResultBoundsAndKeepsLines covers S-03: the read-model
// summary keeps the output's line structure (command output/diffs read line by
// line), collapses only intra-line whitespace, and is bounded by
// maxToolSummaryRunes with the truncation ellipsis.
func TestSummarizeToolResultBoundsAndKeepsLines(t *testing.T) {
	got := summarizeToolResult("first   line\n\n\n\nsecond line\r\nthird")
	if got != "first line\n\nsecond line\nthird" {
		t.Fatalf("summarizeToolResult = %q, want lines kept and whitespace collapsed", got)
	}

	ordered := summarizeToolResult("a\n" + strings.Repeat("b", maxToolSummaryRunes))
	if runes := []rune(ordered); len(runes) != maxToolSummaryRunes+1 || runes[len(runes)-1] != '…' {
		t.Fatalf("bounded summary runes = %d, want %d plus the ellipsis", len(runes), maxToolSummaryRunes+1)
	}
	if !strings.HasPrefix(ordered, "a\n") {
		t.Fatalf("bounded summary lost its first line: %q", ordered[:8])
	}
	if got := summarizeToolResult("  \n\n "); got != "" {
		t.Fatalf("blank result = %q, want empty", got)
	}
}

// TestBoundedToolArguments covers the input bound: the read model and the
// durable tool_call event carry the arguments, bounded but line-preserving.
func TestBoundedToolArguments(t *testing.T) {
	if got := boundedToolArguments(`{"command":"ls"}`); got != `{"command":"ls"}` {
		t.Fatalf("small arguments = %q", got)
	}
	large := strings.Repeat("x", maxToolArgumentsRunes+10)
	if got := boundedToolArguments(large); len([]rune(got)) != maxToolArgumentsRunes+1 ||
		!strings.HasSuffix(got, "…") {
		t.Fatalf("large arguments runes = %d, want the bound plus the ellipsis", len([]rune(got)))
	}
}

// TestApprovalSummaryStaysShort keeps the approval dialog's summary a prompt,
// not a dump: the long-argument block is the dialog's separate parameters area.
func TestApprovalSummaryStaysShort(t *testing.T) {
	got := summarizeToolArguments(strings.Repeat("y", maxApprovalSummaryRunes+50))
	if runes := []rune(got); len(runes) != maxApprovalSummaryRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("approval summary runes = %d, want %d plus the ellipsis", len(runes), maxApprovalSummaryRunes+1)
	}
}
