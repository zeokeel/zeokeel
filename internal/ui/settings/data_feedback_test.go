package settings_test

// TDD tests (RED first) for session-feedback export and diagnostic statistics.

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// seedFeedbackSessions adds one session with a good and a bad rated answer plus
// one session without any feedback.
func seedFeedbackSessions(chat *fakeChat) {
	now := time.Now()
	chat.addSession(contracts.ChatSession{
		ID: "s1", Title: "反馈会话", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Messages: []contracts.ChatMessage{
			{Role: contracts.ChatRoleUser, Text: "问题一"},
			{Role: contracts.ChatRoleAssistant, Text: "有帮助的回答", Feedback: contracts.FeedbackGood},
			{Role: contracts.ChatRoleAssistant, Text: "没帮助的回答", Feedback: contracts.FeedbackBad},
		},
	})
	chat.addSession(contracts.ChatSession{
		ID: "s2", Title: "无反馈会话", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Minute),
		Messages: []contracts.ChatMessage{
			{Role: contracts.ChatRoleUser, Text: "问题二"},
		},
	})
}

func TestDataPrivacyExportFeedbackMarkers(t *testing.T) {
	chat := &fakeChat{}
	seedFeedbackSessions(chat)
	var title, content string
	overrideSavers(t, func(dialogTitle, defaultName, body string) (string, error) {
		title, content = dialogTitle, body
		return "/tmp/sessions.md", nil
	}, nil)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/sessions", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出全部会话")
	if title != "导出全部会话" {
		t.Fatalf("export dialog title = %q, want 导出全部会话", title)
	}

	for _, want := range []string{
		"> 用户反馈：有帮助",
		"> 用户反馈：没帮助",
		"反馈：1 有帮助 / 1 没帮助",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("export content missing %q:\n%s", want, content)
		}
	}
	// The session summary sits under the section title, before the messages.
	if summary, first := strings.Index(content, "反馈：1 有帮助 / 1 没帮助"), strings.Index(content, "问题一"); summary < 0 || first < 0 || summary > first {
		t.Fatalf("summary is not before the messages:\n%s", content)
	}
	// The feedback-free session contributes no summary line (message markers
	// start with "> 用户反馈：", so only line-leading summaries count).
	summaries := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "反馈：") {
			summaries++
		}
	}
	if summaries != 1 {
		t.Fatalf("summary lines = %d, want 1:\n%s", summaries, content)
	}
}

func TestDataPrivacyExportFeedbackEnglish(t *testing.T) {
	chat := &fakeChat{}
	seedFeedbackSessions(chat)
	var title, content string
	overrideSavers(t, func(dialogTitle, defaultName, body string) (string, error) {
		title, content = dialogTitle, body
		return "/tmp/sessions.md", nil
	}, nil)
	h := newLocaleHarness(t, chat, newFakeSettings(nil), settings.Config{}, "en")
	provide(t, h.ctx, contracts.KeyDataDir, "/data/sessions")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Data & privacy")
	h.click(tester, "Export all sessions")
	if title != "Export all sessions" {
		t.Fatalf("export dialog title = %q, want the English Export all sessions", title)
	}

	for _, want := range []string{
		"> User feedback: Helpful",
		"> User feedback: Not helpful",
		"Feedback: 1 helpful / 1 not helpful",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("English export content missing %q:\n%s", want, content)
		}
	}
}

func TestDataPrivacyExportWithoutFeedbackHasNoLines(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	var content string
	overrideSavers(t, func(title, defaultName, body string) (string, error) {
		content = body
		return "/tmp/sessions.md", nil
	}, nil)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/sessions", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出全部会话")
	if strings.Contains(content, "反馈") || strings.Contains(content, "feedback") {
		t.Fatalf("feedback-free export gained feedback lines:\n%s", content)
	}
}

func TestDataPrivacyReportFeedbackStats(t *testing.T) {
	chat := &fakeChat{}
	seedFeedbackSessions(chat)
	var content string
	overrideSavers(t, nil, func(title, defaultName, body string) (string, error) {
		content = body
		return "/tmp/report.txt", nil
	})
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/report", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出诊断报告")
	for _, want := range []string{"反馈：有帮助 1", "反馈：没帮助 1"} {
		if !strings.Contains(content, want) {
			t.Fatalf("diagnostic report missing %q:\n%s", want, content)
		}
	}

	var english string
	chat2 := &fakeChat{}
	seedFeedbackSessions(chat2)
	overrideSavers(t, nil, func(title, defaultName, body string) (string, error) {
		english = body
		return "/tmp/report.txt", nil
	})
	h2 := newLocaleHarness(t, chat2, newFakeSettings(nil), settings.Config{}, "en")
	provide(t, h2.ctx, contracts.KeyDataDir, "/data/report")
	tester2 := h2.tester()
	tester2.Frame()
	h2.click(tester2, "Data & privacy")
	h2.click(tester2, "Export diagnostic report")
	for _, want := range []string{"Feedback: helpful 1", "Feedback: not helpful 1"} {
		if !strings.Contains(english, want) {
			t.Fatalf("English diagnostic report missing %q:\n%s", want, english)
		}
	}
}

func TestDataPrivacyReportFeedbackStatsZero(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	var content string
	overrideSavers(t, nil, func(title, defaultName, body string) (string, error) {
		content = body
		return "/tmp/report.txt", nil
	})
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/report", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出诊断报告")
	for _, want := range []string{"反馈：有帮助 0", "反馈：没帮助 0"} {
		if !strings.Contains(content, want) {
			t.Fatalf("zero-feedback report missing %q:\n%s", want, content)
		}
	}
}
