package regression

// Adversarial audit: message feedback in the data & privacy exports. The
// all-sessions Markdown export adds per-message feedback quotes and a
// helpful/not-helpful summary; the diagnostic report adds the global feedback
// counts. Unknown feedback values must not be exported.

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestFeedbackInExportsAndReport covers the session export summary/quotes and
// the diagnostic report counts, including malformed values being ignored.
func TestFeedbackInExportsAndReport(t *testing.T) {
	chat := newFakeChat()
	base := time.Unix(1_700_000_000, 0)
	chat.addSession(contracts.ChatSession{ID: "s-fb", Title: "反馈会话", CreatedAt: base, UpdatedAt: base,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "有帮助的回答", Feedback: contracts.FeedbackGood},
			{ID: "m3", Role: contracts.ChatRoleAssistant, Text: "没帮助的回答", Feedback: contracts.FeedbackBad},
			{ID: "m4", Role: contracts.ChatRoleAssistant, Text: "畸形值", Feedback: "great"},
		}}, true)

	var sessionsExport, report string
	overrideDataSeams(t, nil,
		func(_, content string) (string, error) { sessionsExport = content; return "/tmp/sessions.md", nil },
		func(_, content string) (string, error) { report = content; return "/tmp/report.txt", nil })
	harness := startDataPrivacy(t, chat)

	// All-sessions Markdown: summary + per-message quotes, invalid ignored.
	if err := harness.tester.Click("导出全部会话"); err != nil {
		t.Fatalf("export all sessions: %v", err)
	}
	harness.tester.Frame()
	for _, want := range []string{"# 反馈会话", "反馈：1 有帮助 / 1 没帮助",
		"> 用户反馈：有帮助", "> 用户反馈：没帮助"} {
		if !strings.Contains(sessionsExport, want) {
			t.Fatalf("sessions export lacks %q:\n%s", want, sessionsExport)
		}
	}
	if strings.Contains(sessionsExport, "> 用户反馈：great") || strings.Contains(sessionsExport, "用户反馈：畸形") {
		t.Fatalf("invalid feedback was exported:\n%s", sessionsExport)
	}

	// Diagnostic report: global feedback counts.
	if err := harness.tester.Click("导出诊断报告"); err != nil {
		t.Fatalf("export diagnostic report: %v", err)
	}
	harness.tester.Frame()
	for _, want := range []string{"反馈：有帮助 1", "反馈：没帮助 1"} {
		if !strings.Contains(report, want) {
			t.Fatalf("diagnostic report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "sk-") {
		t.Fatalf("diagnostic report leaked a secret")
	}
}
