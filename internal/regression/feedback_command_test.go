package regression

// Adversarial audit: the /feedback slash command. Negative/boundary cases: the
// command's Enabled conditions (no SessionFeedbacker, no active session, no
// assistant answer), the dialog's rating/clearing, and the exported feedback
// fields (JSONL + Markdown), including malformed read-model values that must be
// ignored instead of exported.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// feedbackHarness is the session screen over a scripted chat service.
type feedbackHarness struct {
	h      *harness
	chat   contracts.ChatService
	tester *ui.Tester
}

// startFeedbackHarness boots the session screen with chat and one active
// session.
func startFeedbackHarness(t *testing.T, chat contracts.ChatService) *feedbackHarness {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, chat)
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &feedbackHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness
}

// feedbackCommand returns the registered /feedback command.
func feedbackCommand(t *testing.T, harness *feedbackHarness) contracts.CommandSpec {
	t.Helper()
	for _, command := range harness.h.ui.Runtime().Commands() {
		if command.ID == "zeokeel.session.feedback" {
			return command
		}
	}
	t.Fatalf("zeokeel.session.feedback is not registered; commands = %v", harness.h.ui.Runtime().Commands())
	return contracts.CommandSpec{}
}

// message builds one read-model message.
func feedbackMessage(id, role, text, feedback string) contracts.ChatMessage {
	return contracts.ChatMessage{ID: id, Role: role, Text: text, Feedback: feedback,
		CreatedAt: time.Unix(1_700_000_000, 0)}
}

// TestFeedbackCommandEnabledConditions covers the command gate: it is enabled
// only with a feedbacker, an active session and an assistant answer.
func TestFeedbackCommandEnabledConditions(t *testing.T) {
	withAssistant := newFakeChat()
	withAssistant.addSession(contracts.ChatSession{ID: "sess-fb", Title: "反馈",
		Messages: []contracts.ChatMessage{
			feedbackMessage("u1", contracts.ChatRoleUser, "question", ""),
			feedbackMessage("a1", contracts.ChatRoleAssistant, "answer", ""),
		}}, true)
	command := feedbackCommand(t, startFeedbackHarness(t, withAssistant))
	if command.Enabled == nil || !command.Enabled() {
		t.Fatalf("feedback command disabled with a feedbacker, active session and assistant")
	}
	if command.Title != "反馈" || command.Hint != "评价最近一条回答" {
		t.Fatalf("feedback command copy = %q/%q", command.Title, command.Hint)
	}

	noAssistant := newFakeChat()
	noAssistant.addSession(contracts.ChatSession{ID: "sess-q", Title: "无回答",
		Messages: []contracts.ChatMessage{feedbackMessage("u1", contracts.ChatRoleUser, "question", "")}}, true)
	if command := feedbackCommand(t, startFeedbackHarness(t, noAssistant)); command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command enabled without an assistant message")
	}

	noActive := newFakeChat()
	noActive.addSession(contracts.ChatSession{ID: "sess-a", Title: "无活动",
		Messages: []contracts.ChatMessage{feedbackMessage("a1", contracts.ChatRoleAssistant, "answer", "")}}, false)
	if command := feedbackCommand(t, startFeedbackHarness(t, noActive)); command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command enabled without an active session")
	}

	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{ID: "sess-p", Title: "纯聊天",
		Messages: []contracts.ChatMessage{feedbackMessage("a1", contracts.ChatRoleAssistant, "answer", "")}}, true)
	withoutFeedbacker := chatWithoutFeedback{inner: plain}
	if command := feedbackCommand(t, startFeedbackHarness(t, withoutFeedbacker)); command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command enabled without contracts.SessionFeedbacker")
	}
}

// TestFeedbackCommandDialogRateAndClear covers the dialog: it opens for the
// newest assistant answer, ratings go through SessionFeedbacker and the clear
// action removes the rating.
func TestFeedbackCommandDialogRateAndClear(t *testing.T) {
	chat := newFakeChat()
	longAnswer := strings.Repeat("这是一条很长的回答。", 20)
	chat.addSession(contracts.ChatSession{ID: "sess-dialog", Title: "对话框",
		Messages: []contracts.ChatMessage{
			feedbackMessage("u1", contracts.ChatRoleUser, "question", ""),
			feedbackMessage("a1", contracts.ChatRoleAssistant, longAnswer, ""),
		}}, true)
	harness := startFeedbackHarness(t, chat)
	command := feedbackCommand(t, harness)
	if err := command.Run(); err != nil {
		t.Fatalf("Run feedback command: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "评价回答")
	requireHasText(t, harness.tester, "有帮助")
	requireHasText(t, harness.tester, "没帮助")
	requireHasText(t, harness.tester, "保存")
	requireHasText(t, harness.tester, "取消")
	// No rating yet: there is nothing to clear.
	requireNoText(t, harness.tester, "清除评价")
	// The answer preview is truncated in the dialog (the transcript behind the
	// modal still shows the full answer, so assert on the dialog's preview label).
	truncated := string([]rune(longAnswer)[:120]) + "…"
	if !harness.tester.HasText(truncated) {
		t.Fatalf("dialog lacks the truncated answer preview %q; texts = %v", truncated, harness.tester.Texts())
	}

	if err := harness.tester.Click("有帮助"); err != nil {
		t.Fatalf("click Helpful: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	// The rating is a choice in the dialog; Save (the accent primary) commits.
	requireHasText(t, harness.tester, "评价回答")
	if err := harness.tester.Click("保存"); err != nil {
		t.Fatalf("save rating: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "评价回答") // the dialog closes on success
	session, _ := chat.Session("sess-dialog")
	rating := ""
	for _, message := range session.Messages {
		if message.ID == "a1" {
			rating = message.Feedback
		}
	}
	if rating != contracts.FeedbackGood {
		t.Fatalf("assistant Feedback = %q after Helpful, want good", rating)
	}

	// Reopening shows the rated badge; clearing removes the rating.
	if err := command.Run(); err != nil {
		t.Fatalf("Run feedback command again: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "清除评价")
	if err := harness.tester.Click("清除评价"); err != nil {
		t.Fatalf("click Clear rating: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	session, _ = chat.Session("sess-dialog")
	for _, message := range session.Messages {
		if message.ID == "a1" && message.Feedback != "" {
			t.Fatalf("assistant Feedback = %q after clearing, want empty", message.Feedback)
		}
	}
}

// TestFeedbackCommandDisabledRunIsSafe covers calling the disabled command: it
// opens no dialog and does not panic.
func TestFeedbackCommandDisabledRunIsSafe(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-no-assistant", Title: "无回答",
		Messages: []contracts.ChatMessage{feedbackMessage("u1", contracts.ChatRoleUser, "question", "")}}, true)
	harness := startFeedbackHarness(t, chat)
	command := feedbackCommand(t, harness)
	if command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command is enabled without an assistant message")
	}
	if err := command.Run(); err != nil {
		t.Fatalf("Run disabled feedback command: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "评价回答")
}

// TestFeedbackCommandSaveFailureKeepsDialog covers a backend write failure: the
// dialog stays open with the localized error and the read model is unchanged.
func TestFeedbackCommandSaveFailureKeepsDialog(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-fail", Title: "保存失败",
		Messages: []contracts.ChatMessage{
			feedbackMessage("u1", contracts.ChatRoleUser, "question", ""),
			feedbackMessage("a1", contracts.ChatRoleAssistant, "answer", ""),
		}}, true)
	chat.setFeedbackError(errors.New("settings write denied"))
	harness := startFeedbackHarness(t, chat)
	command := feedbackCommand(t, harness)
	if err := command.Run(); err != nil {
		t.Fatalf("Run feedback command: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("有帮助"); err != nil {
		t.Fatalf("click Helpful: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	if err := harness.tester.Click("保存"); err != nil {
		t.Fatalf("save rating: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "评价回答") // the dialog stays open to retry
	requireHasText(t, harness.tester, "反馈保存失败：settings write denied")
	session, _ := chat.Session("sess-fail")
	for _, message := range session.Messages {
		if message.ID == "a1" && message.Feedback != "" {
			t.Fatalf("failed write changed the read model: %q", message.Feedback)
		}
	}
}

// exportSeams installs capturing Markdown/JSONL save seams for one test.
func exportSeams(t *testing.T) *string {
	t.Helper()
	var captured string
	originalMarkdown := sessionui.ExportSaver
	originalJSONL := sessionui.ExportJSONLSaver
	saver := func(title, name, content string) (string, error) {
		captured = content
		return "/tmp/" + name, nil
	}
	sessionui.ExportSaver = saver
	sessionui.ExportJSONLSaver = saver
	t.Cleanup(func() {
		sessionui.ExportSaver = originalMarkdown
		sessionui.ExportJSONLSaver = originalJSONL
	})
	return &captured
}

// exportViaMenu opens the session header overflow menu and clicks one export.
func exportViaMenu(t *testing.T, harness *feedbackHarness, menuLabel, itemLabel string) {
	t.Helper()
	if err := harness.tester.Click(menuLabel); err != nil {
		t.Fatalf("open export menu: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	if err := harness.tester.Click(itemLabel); err != nil {
		t.Fatalf("click %q: %v; texts = %v", itemLabel, err, harness.tester.Texts())
	}
	harness.tester.Frame()
}

// jsonlMessageRecords parses the JSONL export into message records.
func jsonlMessageRecords(t *testing.T, content string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	records := make([]map[string]any, 0, len(lines))
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("JSONL line %d is not JSON: %v", index, err)
		}
		if record["type"] == "message" {
			records = append(records, record)
		}
	}
	return records
}

// TestFeedbackExportFields covers the exported rating fields: JSONL includes
// only valid ratings (good/bad) and Markdown renders the localized feedback line;
// malformed read-model values are ignored in both formats.
func TestFeedbackExportFields(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-export", Title: "导出反馈",
		Messages: []contracts.ChatMessage{
			feedbackMessage("u1", contracts.ChatRoleUser, "question", ""),
			feedbackMessage("a1", contracts.ChatRoleAssistant, "good answer", contracts.FeedbackGood),
			feedbackMessage("a2", contracts.ChatRoleAssistant, "bad answer", contracts.FeedbackBad),
			feedbackMessage("a3", contracts.ChatRoleAssistant, "malformed answer", "great"),
		}}, true)
	harness := startFeedbackHarness(t, chat)

	// JSONL: good/bad exported, malformed ignored.
	captured := exportSeams(t)
	exportViaMenu(t, harness, "会话操作", "导出 JSONL")
	records := jsonlMessageRecords(t, *captured)
	if len(records) != 4 {
		t.Fatalf("JSONL message records = %d, want 4", len(records))
	}
	if got := records[1]["feedback"]; got != contracts.FeedbackGood {
		t.Fatalf("good record feedback = %v, want good", got)
	}
	if got := records[2]["feedback"]; got != contracts.FeedbackBad {
		t.Fatalf("bad record feedback = %v, want bad", got)
	}
	if _, ok := records[3]["feedback"]; ok {
		t.Fatalf("malformed feedback was exported: %v", records[3]["feedback"])
	}

	// Markdown (zh): the feedback quote line appears for good/bad only.
	exportViaMenu(t, harness, "会话操作", "导出 Markdown")
	if !strings.Contains(*captured, "> 用户反馈：有帮助") {
		t.Fatalf("Markdown lacks the good feedback line: %q", *captured)
	}
	if !strings.Contains(*captured, "> 用户反馈：没帮助") {
		t.Fatalf("Markdown lacks the bad feedback line")
	}
	if strings.Contains(*captured, "malformed answer") && strings.Contains(*captured, "用户反馈：great") {
		t.Fatalf("Markdown exported a malformed rating")
	}

	// English copy: "User feedback: ...".
	harness.h.ui.Locale().SetLanguage("en")
	harness.tester.Frame()
	exportViaMenu(t, harness, "Conversation actions", "Export Markdown")
	if !strings.Contains(*captured, "User feedback: Helpful") || !strings.Contains(*captured, "User feedback: Not helpful") {
		t.Fatalf("English Markdown lacks the feedback lines: %q", *captured)
	}
}
