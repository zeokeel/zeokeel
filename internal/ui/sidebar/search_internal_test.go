package sidebar

// Internal tests for the message-search snippet window and the highlight
// spans: the row must cut around the first match instead of showing the
// message's start (R11/B9).

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/design"
)

func testTokens() *design.Tokens { return design.Light() }

func TestSnippetWindowCentersOnMatch(t *testing.T) {
	long := strings.Repeat("前言", 40) + "工作目录" + strings.Repeat("后文", 60)
	window := snippetWindow(long, "工作目录")
	if !strings.Contains(window, "工作目录") {
		t.Fatalf("snippet window %q does not contain the match", window)
	}
	before := []rune(window[:strings.Index(window, "工作目录")])
	if len(before) > snippetLead+1 {
		t.Fatalf("snippet window %q keeps %d runes before the match, want at most %d",
			window, len(before), snippetLead+1)
	}
	if !strings.HasPrefix(window, "…") || !strings.HasSuffix(window, "…") {
		t.Fatalf("snippet window %q does not mark both cuts with an ellipsis", window)
	}
}

func TestSnippetWindowKeepsShortSnippet(t *testing.T) {
	if got := snippetWindow("如何修复登录问题", "登录"); got != "如何修复登录问题" {
		t.Fatalf("short snippet = %q, want it unchanged", got)
	}
}

func TestSnippetWindowWithoutMatch(t *testing.T) {
	if got := snippetWindow("先检查认证模块", "登录"); got != "先检查认证模块" {
		t.Fatalf("snippet without the query = %q, want it unchanged", got)
	}
}

func TestHighlightSpansMarksEveryMatch(t *testing.T) {
	spans := highlightSpans("登录与登录问题", "登录", testTokens())
	if len(spans) != 4 {
		t.Fatalf("highlight spans = %d (%+v), want 4 (two matches and two gaps)", len(spans), spans)
	}
	highlighted := 0
	for _, span := range spans {
		if span.Text == "登录" {
			highlighted++
			if span.Weight != 600 {
				t.Fatalf("match span weight = %d, want 600", span.Weight)
			}
		}
	}
	if highlighted != 2 {
		t.Fatalf("emphasized matches = %d, want 2; spans = %+v", highlighted, spans)
	}
}

func TestHighlightSpansCaseInsensitive(t *testing.T) {
	spans := highlightSpans("Project Alpha", "p", testTokens())
	if len(spans) == 0 {
		t.Fatal("highlight spans = nil, want the case-insensitive match")
	}
	found := false
	for _, span := range spans {
		if strings.EqualFold(span.Text, "p") && span.Weight == 600 {
			found = true
		}
	}
	if !found {
		t.Fatalf("case-insensitive match is not emphasized; spans = %+v", spans)
	}
}
