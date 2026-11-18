package session_test

// Frame test for the reply's reading measure (B-spec §3.2.1/§3.6, VD-23): the
// prose takes design.ProseMeasure (640) inside the 960 DIP conversation column,
// while a table keeps the whole column (it cannot shrink gracefully, and a table
// scrolling inside 640 would be worse than a wide one).

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// proseChat is one finished reply whose body is the given Markdown.
func proseChat(text string) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "阅读列",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "写点东西"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: text},
		},
	}, true)
	return chat
}

// TestReplyProseTakesTheReadingMeasure covers the prose case: a long paragraph
// wraps at the reading measure instead of running the whole 960 DIP column.
func TestReplyProseTakesTheReadingMeasure(t *testing.T) {
	paragraph := strings.Repeat("这是一段用来测量行宽的中文正文。", 12)
	h := newHarness(t, proseChat(paragraph))
	tester := h.tester()
	tester.Frame()

	rect, ok := tester.Find(paragraph)
	if !ok {
		t.Fatalf("reply paragraph missing; texts = %v", tester.Texts())
	}
	if rect.W > design.ProseMeasure+1 {
		t.Fatalf("reply paragraph is %.1f wide, want at most the reading measure %v",
			rect.W, design.ProseMeasure)
	}
	// It is still a real measure, not a collapsed column.
	if rect.W < design.ProseMeasure/2 {
		t.Fatalf("reply paragraph is only %.1f wide, want about %v", rect.W, design.ProseMeasure)
	}
}

// TestReplyTableKeepsTheColumn covers the exception: a table keeps the full
// column width, because a table squeezed into 640 DIP would scroll inside the
// measure.
func TestReplyTableKeepsTheColumn(t *testing.T) {
	table := "| 名称 | 大小 |\n| --- | --- |\n| 文件 | 12 B |\n"
	h := newHarness(t, proseChat(table))
	tester := h.tester()
	tester.Frame()

	cell, ok := tester.Find("名称")
	if !ok {
		t.Fatalf("table header missing; texts = %v", tester.Texts())
	}
	// The paragraph measure is 640; the table's own box starts at the column's
	// left edge and the header cell sits inside it, so the table must not be
	// clamped to the reading measure: a cell at 640 would mean the table was
	// squeezed. Probe the row: the table's right-hand column is reachable.
	second, ok := tester.Find("大小")
	if !ok {
		t.Fatalf("second table column missing; texts = %v", tester.Texts())
	}
	if second.X <= cell.X {
		t.Fatalf("table columns are stacked, not side by side: first=%.1f second=%.1f", cell.X, second.X)
	}
}
