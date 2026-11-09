package session_test

// Tests for the /feedback slash command, its dialog and feedback in exports.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func feedbackChatWith(messages ...contracts.ChatMessage) *feedbackChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "Feedback", Messages: messages}, true)
	return &feedbackChat{fakeChat: chat}
}

func feedbackCommand(t *testing.T, h *harness) contracts.CommandSpec {
	t.Helper()
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == "zeokeel.session.feedback" {
			return command
		}
	}
	t.Fatalf("zeokeel.session.feedback is not registered; commands = %v", h.ui.Runtime().Commands())
	return contracts.CommandSpec{}
}

func TestFeedbackCommandEnabledConditions(t *testing.T) {
	withAssistant := feedbackChatWith(
		contracts.ChatMessage{ID: "u1", Role: contracts.ChatRoleUser, Text: "q"},
		contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "a"},
	)
	h := newFeedbackHarness(t, withAssistant)
	command := feedbackCommand(t, h)
	if command.Title != "反馈" || command.Hint != "评价最近一条回答" {
		t.Fatalf("feedback command = %q/%q", command.Title, command.Hint)
	}
	if command.Enabled == nil || !command.Enabled() {
		t.Fatal("feedback command disabled with a feedbacker, active session and assistant")
	}

	noAssistant := feedbackChatWith(contracts.ChatMessage{ID: "u1", Role: contracts.ChatRoleUser, Text: "q"})
	h = newFeedbackHarness(t, noAssistant)
	if command := feedbackCommand(t, h); command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command enabled without an assistant message")
	}

	noActive := feedbackChatWith(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "a"})
	noActive.fakeChat.active = ""
	h = newFeedbackHarness(t, noActive)
	if command := feedbackCommand(t, h); command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command enabled without an active session")
	}

	// Without SessionFeedbacker the command stays hidden.
	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{ID: "sess-1", Messages: []contracts.ChatMessage{
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "a"},
	}}, true)
	h = newHarness(t, plain)
	if command := feedbackCommand(t, h); command.Enabled == nil || command.Enabled() {
		t.Fatal("feedback command enabled without SessionFeedbacker")
	}
}

func TestFeedbackDialogRendersAndRates(t *testing.T) {
	longText := strings.Repeat("这是一条很长的回答。", 20)
	chat := feedbackChatWith(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: longText})
	h := newFeedbackHarness(t, chat)
	command := feedbackCommand(t, h)
	if err := command.Run(); err != nil {
		t.Fatalf("Run feedback command: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "评价回答", "有帮助", "没帮助", "保存", "取消")
	if !tester.HasText("这是一条很长的回答。…") && !tester.HasText("…") {
		t.Fatalf("truncated answer summary missing; texts = %v", tester.Texts())
	}

	// Choosing a rating then saving closes the dialog and records the ids.
	h.click(tester, "有帮助")
	h.click(tester, "保存")
	call := chat.lastCall(t)
	if call.sessionID != "sess-1" || call.messageID != "a1" || call.feedback != contracts.FeedbackGood {
		t.Fatalf("good call = %+v", call)
	}
	h.assertNoTexts(tester, "评价回答")

	// Bad and clear from reopened dialogs.
	if err := feedbackCommand(t, h).Run(); err != nil {
		t.Fatalf("Run feedback again: %v", err)
	}
	tester.Frame()
	h.click(tester, "没帮助")
	h.click(tester, "保存")
	if call := chat.lastCall(t); call.feedback != contracts.FeedbackBad {
		t.Fatalf("bad call = %+v", call)
	}
	if err := feedbackCommand(t, h).Run(); err != nil {
		t.Fatalf("Run feedback third time: %v", err)
	}
	tester.Frame()
	h.click(tester, "清除评价")
	if call := chat.lastCall(t); call.feedback != "" {
		t.Fatalf("clear call = %+v", call)
	}
}

func TestFeedbackDialogErrorAndCancel(t *testing.T) {
	chat := feedbackChatWith(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "answer"})
	chat.setErr = errors.New("feedback write failed")
	h := newFeedbackHarness(t, chat)
	if err := feedbackCommand(t, h).Run(); err != nil {
		t.Fatalf("Run feedback command: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	h.click(tester, "有帮助")
	h.click(tester, "保存")
	h.assertTexts(tester, "反馈保存失败：feedback write failed", "评价回答")

	chat.mu.Lock()
	chat.setErr = nil
	chat.mu.Unlock()
	h.click(tester, "取消")
	h.assertNoTexts(tester, "评价回答")
}

func TestFeedbackDialogEnglish(t *testing.T) {
	chat := feedbackChatWith(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "answer"})
	h := newFeedbackHarness(t, chat)
	h.ui.Locale().SetLanguage("en")
	if err := feedbackCommand(t, h).Run(); err != nil {
		t.Fatalf("Run feedback command: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Rate answer", "Helpful", "Not helpful", "Save", "Cancel")
	h.click(tester, "Helpful")
	h.click(tester, "Save")
	if call := chat.lastCall(t); call.feedback != contracts.FeedbackGood {
		t.Fatalf("English good call = %+v", call)
	}
}

// --- exports ----------------------------------------------------------------

func feedbackExportChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-export", Title: "反馈导出",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答一", Feedback: contracts.FeedbackGood},
			{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "回答二", Feedback: contracts.FeedbackBad},
			{ID: "a3", Role: contracts.ChatRoleAssistant, Text: "回答三", Feedback: "invalid"},
		},
	}, true)
	return chat
}

func TestJSONLExportIncludesFeedback(t *testing.T) {
	chat := feedbackExportChat()
	var content string
	withJSONLSeam(t, func(title, name, data string) (string, error) {
		content = data
		return "/tmp/export.jsonl", nil
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.click(tester, "更多")
	h.click(tester, "导出 JSONL")
	tester.Frame()
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) != 5 {
		t.Fatalf("JSONL lines = %d:\n%s", len(lines), content)
	}
	feedbacks := map[string]any{}
	for _, line := range lines[1:] {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if id, ok := record["id"].(string); ok {
			if value, exists := record["feedback"]; exists {
				feedbacks[id] = value
			}
		}
	}
	if feedbacks["a1"] != contracts.FeedbackGood || feedbacks["a2"] != contracts.FeedbackBad {
		t.Fatalf("JSONL feedbacks = %v", feedbacks)
	}
	if _, exists := feedbacks["a3"]; exists {
		t.Fatalf("invalid feedback exported: %v", feedbacks["a3"])
	}
	if _, exists := feedbacks["u1"]; exists {
		t.Fatalf("user message got a feedback field: %v", feedbacks)
	}
}

func TestMarkdownExportIncludesFeedback(t *testing.T) {
	var content string
	setExportSaver(t, func(title, name, data string) (string, error) {
		content = data
		return "/tmp/export.md", nil
	})
	h := newHarness(t, feedbackExportChat())
	tester := h.tester()
	h.click(tester, "更多")
	h.click(tester, "导出 Markdown")
	if !strings.Contains(content, "> 用户反馈：有帮助") || !strings.Contains(content, "> 用户反馈：没帮助") {
		t.Fatalf("Markdown feedback lines missing:\n%s", content)
	}
	if strings.Count(content, "> 用户反馈：") != 2 {
		t.Fatalf("invalid feedback was exported:\n%s", content)
	}

	// English export uses the English feedback copy.
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "More")
	h.click(tester, "Export Markdown")
	if !strings.Contains(content, "> User feedback: Helpful") || !strings.Contains(content, "> User feedback: Not helpful") {
		t.Fatalf("English Markdown feedback lines missing:\n%s", content)
	}
}

// TestFeedbackDialogUsesToggleGroup covers the /feedback rating control: two
// official toggles in an official ToggleGroup (a group named 评价), pressed
// semantics, mutual exclusion, and pressing the pressed one again clearing the
// choice (both unpressed = 未评价, which Save writes).
func TestFeedbackDialogUsesToggleGroup(t *testing.T) {
	chat := feedbackChatWith(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "answer"})
	h := newFeedbackHarness(t, chat)
	if err := feedbackCommand(t, h).Run(); err != nil {
		t.Fatalf("Run feedback command: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleGroup, "评价"); !ok {
		t.Fatalf("rating ToggleGroup missing; texts = %v", tester.Texts())
	}
	good, ok := tester.AccessByRole(ui.RoleToggleButton, "有帮助")
	if !ok || good.Checked {
		t.Fatalf("unrated dialog opened with good pressed: %+v found %v", good, ok)
	}
	if info, ok := tester.AccessByRole(ui.RoleToggleButton, "没帮助"); !ok || info.Checked {
		t.Fatalf("unrated dialog opened with bad pressed: %+v found %v", info, ok)
	}

	h.click(tester, "有帮助")
	if info, _ := tester.AccessByRole(ui.RoleToggleButton, "有帮助"); !info.Checked {
		t.Fatalf("good toggle not pressed after clicking: %+v", info)
	}
	h.click(tester, "没帮助")
	if info, _ := tester.AccessByRole(ui.RoleToggleButton, "有帮助"); info.Checked {
		t.Fatalf("good toggle stayed pressed after choosing bad: %+v", info)
	}
	if info, _ := tester.AccessByRole(ui.RoleToggleButton, "没帮助"); !info.Checked {
		t.Fatalf("bad toggle not pressed after clicking: %+v", info)
	}
	// Clicking the pressed one clears both: the dialog is back to 未评价, so
	// Save has nothing to write (unchanged unrated message).
	h.click(tester, "没帮助")
	if info, _ := tester.AccessByRole(ui.RoleToggleButton, "没帮助"); info.Checked {
		t.Fatalf("bad toggle stayed pressed after clicking it again: %+v", info)
	}
	h.click(tester, "取消")

	// A rated message opens with its rating pressed; pressing it again clears
	// the rating and Save writes the clear.
	rated := feedbackChatWith(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "answer", Feedback: contracts.FeedbackGood,
	})
	h = newFeedbackHarness(t, rated)
	if err := feedbackCommand(t, h).Run(); err != nil {
		t.Fatalf("Run feedback command on a rated message: %v", err)
	}
	tester = h.tester()
	tester.Frame()
	if info, ok := tester.AccessByRole(ui.RoleToggleButton, "有帮助"); !ok || !info.Checked {
		t.Fatalf("rated message did not open with good pressed: %+v found %v", info, ok)
	}
	h.click(tester, "有帮助")
	if info, _ := tester.AccessByRole(ui.RoleToggleButton, "有帮助"); info.Checked {
		t.Fatalf("good toggle stayed pressed after clicking it again: %+v", info)
	}
	h.click(tester, "保存")
	if call := rated.lastCall(t); call.feedback != "" {
		t.Fatalf("save after clearing = %+v, want a cleared rating", call)
	}
}
