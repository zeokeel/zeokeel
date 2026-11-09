package home_test

// Conformance frame tests for the v0.8.3 home fixes: the recent-conversation
// rows are a mygo List (Enter opens the chosen row, a click opens the row) and
// the row date follows the interface language.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// recentChat is the Home harness chat with two recent conversations.
func recentChat(base time.Time) *composerChat {
	return &composerChat{fakeChat: &fakeChat{
		config: contracts.ChatConfig{Model: "test-model"},
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "最近会话一", UpdatedAt: base.Add(2 * time.Hour)},
			{ID: "s2", Title: "第二会话", UpdatedAt: base.Add(time.Hour)},
		},
	}}
}

func openRecentHome(t *testing.T, chat *composerChat) (*harness, *ui.Tester) {
	t.Helper()
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	return h, tester
}

// TestHomeRecentListClickAndEnter covers the List: a click opens a recent
// conversation, and Enter opens the row chosen with the keyboard.
func TestHomeRecentListClickAndEnter(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := recentChat(base)
	_, tester := openRecentHome(t, chat)
	if err := tester.Click("第二会话"); err != nil {
		t.Fatalf("click recent row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := chat.activeIDs; len(got) != 1 || got[0] != "s2" {
		t.Fatalf("click opened %v, want [s2]", got)
	}

	// Tab into the recent List; Up chooses its first row and Enter opens it
	// (the click above left the second row chosen).
	chat.activeIDs = nil
	if !tabUntil(t, tester, "最近会话一", 80) {
		t.Fatalf("the recent list never took the keyboard focus; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := chat.activeIDs; len(got) != 1 || got[0] != "s1" {
		t.Fatalf("Enter opened %v, want [s1]", got)
	}
}

// tabUntil presses Tab until the given label's control has the keyboard focus.
func tabUntil(t *testing.T, tester *ui.Tester, label string, limit int) bool {
	t.Helper()
	for range limit {
		if tester.Focused(label) {
			return true
		}
		tester.Key(0, ui.KeyTab)
		tester.Frame()
	}
	return tester.Focused(label)
}

// TestHomeRecentDatesFollowLocale covers the FormatDateTime fix: the row date
// is the locale's readable form, not the fixed Chinese one.
func TestHomeRecentDatesFollowLocale(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := recentChat(base)
	h := newComposerHarness(t, chat, nil, nil)
	h.ui.Locale().SetLanguage("en")
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	want := uikit.FormatDateTime(h.ui.Locale(), base.Add(2*time.Hour))
	if !tester.HasText(want) {
		t.Fatalf("English recent date %q missing; texts = %v", want, tester.Texts())
	}
	zh := uikit.FormatDateTime(nil, base.Add(2*time.Hour))
	if want == zh {
		t.Fatalf("FormatDateTime ignored the locale: %q", want)
	}
	if tester.HasText(zh) {
		t.Fatalf("recent row still shows the Chinese date %q", zh)
	}
}
