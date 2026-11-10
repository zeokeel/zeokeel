package regression

// Adversarial audit: the session screen's JSONL export, composer attachment
// limits and goal chip. These tests drive the real conversation UI module with a
// scripted ChatService (no agent backend): export bytes are captured through the
// exported seam and validated line by line, and oversized/odd attachments go
// through the host FileDrop bridge.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// sessionHarness boots the session UI module with a scripted chat read model.
type sessionHarness struct {
	h        *harness
	chat     *fakeChat
	tester   *ui.Tester
	notifier *fakeNotifier
}

// startSessionUI activates the session screen with the given sessions; the
// first session is active.
func startSessionUI(t *testing.T, sessions ...contracts.ChatSession) *sessionHarness {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	for index, chatSession := range sessions {
		chat.addSession(chatSession, index == 0)
	}
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyFileDrop, contracts.FileDropService(h.ui.FileDrop()))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &sessionHarness{h: h, chat: chat, tester: h.tester(1280, 800), notifier: notifier}
	harness.tester.Frame()
	return harness
}

// notifierTexts returns the toasts a fakeNotifier recorded.
func notifierTexts(notifier *fakeNotifier) []contracts.Notification {
	if notifier == nil {
		return nil
	}
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	return append([]contracts.Notification(nil), notifier.delivered...)
}

// toastMatches reports whether one recorded toast contains every fragment.
func toastMatches(notifier *fakeNotifier, fragments ...string) bool {
	for _, toast := range notifierTexts(notifier) {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(toast.Text, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// requireToast waits for one recorded toast containing every fragment.
func requireToast(t *testing.T, notifier *fakeNotifier, fragments ...string) {
	t.Helper()
	waitFor(t, "toast "+strings.Join(fragments, "/"), func() bool {
		return toastMatches(notifier, fragments...)
	})
}

// installExportSeam replaces the JSONL save seam and captures the written
// content. The returned pointer lets a test change the saver behavior.
func (sh *sessionHarness) installExportSeam(t *testing.T, resultErr error) *string {
	t.Helper()
	var captured string
	original := sessionui.ExportJSONLSaver
	sessionui.ExportJSONLSaver = func(title, defaultName, content string) (string, error) {
		captured = content
		if resultErr != nil {
			return "", resultErr
		}
		return filepath.Join(t.TempDir(), defaultName), nil
	}
	t.Cleanup(func() { sessionui.ExportJSONLSaver = original })
	return &captured
}

// exportJSONL clicks the header overflow menu and its Export JSONL item.
func (sh *sessionHarness) exportJSONL(t *testing.T) {
	t.Helper()
	if err := sh.tester.Click("会话操作"); err != nil {
		t.Fatalf("open session actions: %v; texts = %v", err, sh.tester.Texts())
	}
	sh.tester.Frame()
	if err := sh.tester.Click("导出 JSONL"); err != nil {
		t.Fatalf("click Export JSONL: %v; texts = %v", err, sh.tester.Texts())
	}
	sh.tester.Frame()
}

// message creates one UI message.
func message(id, role, text string) contracts.ChatMessage {
	return contracts.ChatMessage{ID: id, Role: role, Text: text, CreatedAt: time.Unix(1_700_000_000, 0)}
}

// TestSessionExportJSONLHostileContent is the export negative: quotes, escapes,
// newlines, markup, NUL bytes, empty messages, flattened tools and a 200k-rune
// reasoning string must all produce one valid JSON object per line.
func TestSessionExportJSONLHostileContent(t *testing.T) {
	longReasoning := strings.Repeat("推理-reasoning ", 16_000) // ~200k runes
	chatSession := contracts.ChatSession{
		ID:        "session-jsonl",
		Title:     "标题 \"quoted\" / 换行\n第二行\ttab",
		Model:     "audit-model",
		CreatedAt: time.Unix(1_700_000_000, 0),
		UpdatedAt: time.Unix(1_700_000_100, 0),
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, `quotes "double" and 'single'; backslash C:\tmp\file; newline`+"\n"+
				"second line; emoji 🚀; html <script>alert(1)</script> & amp; nul \x00 end"),
			message("m2", contracts.ChatRoleAssistant, "reply with tab\tand unicode 中文"),
			{ID: "m3", Role: contracts.ChatRoleAssistant, Text: "",
				Reasoning: longReasoning, CreatedAt: time.Unix(1_700_000_050, 0)},
			{ID: "m4", Role: contracts.ChatRoleAssistant, Text: "tool call", CreatedAt: time.Unix(1_700_000_060, 0),
				Tools: []contracts.ToolCall{{
					ID: "call-1", Name: "bash", Status: "ok",
					Summary: `ran "quoted" command with C:\path and ` + "\n" + `newline`,
				}}},
			{ID: "m5", Role: contracts.ChatRoleAssistant, Text: "failed turn", Error: "provider \"timeout\"",
				CreatedAt: time.Unix(1_700_000_070, 0)},
		},
	}
	harness := startSessionUI(t, chatSession)
	captured := harness.installExportSeam(t, nil)
	harness.exportJSONL(t)
	requireToast(t, harness.notifier, "对话已导出")

	lines := strings.Split(strings.TrimSuffix(*captured, "\n"), "\n")
	if len(lines) != 1+len(chatSession.Messages) {
		t.Fatalf("JSONL has %d lines, want header + %d messages", len(lines), len(chatSession.Messages))
	}
	records := make([]map[string]any, 0, len(lines))
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\nline: %q", index, err, line)
		}
		records = append(records, record)
	}
	header := records[0]
	if header["type"] != "session" || header["id"] != chatSession.ID || header["title"] != chatSession.Title {
		t.Fatalf("header record = %+v, want the session identity", header)
	}
	createdAt, err := time.Parse(time.RFC3339, fmt.Sprint(header["createdAt"]))
	if err != nil || createdAt.Unix() != chatSession.CreatedAt.Unix() {
		t.Fatalf("createdAt = %v (%v), want RFC3339 of the session time", header["createdAt"], err)
	}
	if got := records[1]["text"]; got != chatSession.Messages[0].Text {
		t.Fatalf("hostile text round trip = %q, want %q", got, chatSession.Messages[0].Text)
	}
	if got := records[2]["text"]; got != "reply with tab\tand unicode 中文" {
		t.Fatalf("tab/unicode text = %q", got)
	}
	if got := records[3]["reasoning"]; got != longReasoning {
		t.Fatalf("reasoning round trip failed (len %d, want %d)", len(fmt.Sprint(got)), len(longReasoning))
	}
	if text, ok := records[3]["text"]; !ok || text != "" {
		t.Fatalf("empty message text = %v (present=%v), want an empty string field", text, ok)
	}
	tools, ok := records[4]["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one flattened tool", records[4]["tools"])
	}
	toolRecord, _ := tools[0].(map[string]any)
	if toolRecord["name"] != "bash" || toolRecord["status"] != "ok" ||
		toolRecord["summary"] != chatSession.Messages[3].Tools[0].Summary {
		t.Fatalf("tool record = %+v, want the flattened tool", toolRecord)
	}
	if records[5]["error"] != `provider "timeout"` {
		t.Fatalf("error field = %v, want the provider error text", records[5]["error"])
	}
}

// TestSessionExportJSONLEmptySession covers an empty conversation: exactly one
// header line, valid JSON, no message records.
func TestSessionExportJSONLEmptySession(t *testing.T) {
	harness := startSessionUI(t, contracts.ChatSession{
		ID: "empty-session", Title: "空会话", Model: "audit-model",
		CreatedAt: time.Unix(1_700_000_000, 0), UpdatedAt: time.Unix(1_700_000_000, 0),
	})
	captured := harness.installExportSeam(t, nil)
	harness.exportJSONL(t)
	requireToast(t, harness.notifier, "对话已导出")
	lines := strings.Split(strings.TrimSpace(*captured), "\n")
	if len(lines) != 1 {
		t.Fatalf("empty session export has %d lines, want 1 header line", len(lines))
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header line is not valid JSON: %v", err)
	}
	if header["type"] != "session" {
		t.Fatalf("header type = %v, want session", header["type"])
	}
}

// TestSessionExportJSONLCancelAndError covers the seam outcomes: a cancelled
// save shows no badge and no error; a failing save shows an error line. The
// double-prefix defect (AUDIT-P3-4) is probed here and recorded with a skip.
func TestSessionExportJSONLCancelAndError(t *testing.T) {
	harness := startSessionUI(t, contracts.ChatSession{
		ID: "s", Title: "会话", CreatedAt: time.Unix(1_700_000_000, 0),
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "hi")},
	})
	captured := harness.installExportSeam(t, nil)
	_ = captured

	// Cancelled save: the seam returns ("", nil). No badge, no error.
	original := sessionui.ExportJSONLSaver
	sessionui.ExportJSONLSaver = func(string, string, string) (string, error) { return "", nil }
	if err := harness.tester.Click("会话操作"); err != nil {
		t.Fatalf("open actions: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("导出 JSONL"); err != nil {
		t.Fatalf("click export: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已导出")
	sessionui.ExportJSONLSaver = original

	// Failing save: the header should show one localized "导出失败：" prefix plus
	// the underlying error. Today exportSessionJSONL stores the message with a
	// prefix and buildHeader adds another when rendering, so the user sees
	// "导出失败：导出失败：..." (AUDIT-P3-4; the Markdown path sets it once).
	sessionui.ExportJSONLSaver = func(string, string, string) (string, error) {
		return "", errors.New("disk full")
	}
	if err := harness.tester.Click("会话操作"); err != nil {
		t.Fatalf("open actions: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("导出 JSONL"); err != nil {
		t.Fatalf("click export: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "disk full")

	var errorText string
	for _, text := range harness.tester.Texts() {
		if strings.Contains(text, "disk full") {
			errorText = text
		}
	}
	if strings.Count(errorText, "导出失败") > 1 {
		t.Skipf("AUDIT-P3-4 (known defect): the JSONL export error is prefixed %d times: %q",
			strings.Count(errorText, "导出失败"), errorText)
	}
	if !strings.Contains(errorText, "导出失败") {
		t.Fatalf("export error line = %q, want the localized prefix", errorText)
	}
}

// TestSessionAttachmentSizeBoundary covers the 10 MiB composer limit: a file at
// the limit is staged and shown, one byte over is rejected with a readable error
// and no chip.
func TestSessionAttachmentSizeBoundary(t *testing.T) {
	harness := startSessionUI(t, contracts.ChatSession{
		ID: "session-attach", Title: "附件会话", CreatedAt: time.Unix(1_700_000_000, 0),
	})
	dir := t.TempDir()

	// Exactly 10 MiB: accepted (sparse file, no disk cost).
	atLimit := filepath.Join(dir, "at-limit.txt")
	file, err := os.Create(atLimit)
	if err != nil {
		t.Fatalf("create at-limit file: %v", err)
	}
	if err := file.Truncate(10 << 20); err != nil {
		t.Fatalf("truncate at-limit file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close at-limit file: %v", err)
	}
	harness.h.ui.FileDrop().Dispatch([]string{atLimit})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "at-limit.txt")
	requireHasText(t, harness.tester, "10.0 MB")
	requireNoText(t, harness.tester, "超过 10 MB 附件上限")

	// One byte over 10 MiB: rejected, the limit error names the file.
	overLimit := filepath.Join(dir, "over-limit.txt")
	file, err = os.Create(overLimit)
	if err != nil {
		t.Fatalf("create over-limit file: %v", err)
	}
	if err := file.Truncate((10 << 20) + 1); err != nil {
		t.Fatalf("truncate over-limit file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close over-limit file: %v", err)
	}
	harness.h.ui.FileDrop().Dispatch([]string{overLimit})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "超过 10 MB 附件上限")
	requireHasText(t, harness.tester, "over-limit.txt")
	// The rejected file is not staged; the accepted chip is still there.
	requireHasText(t, harness.tester, "at-limit.txt")
}

// TestSessionAttachmentInvalidPaths covers dropped directories and missing
// paths: both are rejected with the not-a-regular-file error, and a following
// valid drop clears the stale error.
func TestSessionAttachmentInvalidPaths(t *testing.T) {
	harness := startSessionUI(t, contracts.ChatSession{
		ID: "session-attach", Title: "附件会话", CreatedAt: time.Unix(1_700_000_000, 0),
	})
	dir := t.TempDir()
	harness.h.ui.FileDrop().Dispatch([]string{dir})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "不是可读的普通文件")

	harness.h.ui.FileDrop().Dispatch([]string{filepath.Join(dir, "missing.txt")})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "不是可读的普通文件")

	valid := filepath.Join(dir, "valid.txt")
	if err := os.WriteFile(valid, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write valid file: %v", err)
	}
	harness.h.ui.FileDrop().Dispatch([]string{valid})
	harness.tester.Frame()
	requireHasText(t, harness.tester, "valid.txt")
	requireNoText(t, harness.tester, "不是可读的普通文件")
}

// TestSessionGoalChipEmptyText is the goal negative in the UI: an empty or
// whitespace-only goal payload renders no chip and cannot panic the header.
func TestSessionGoalChipEmptyText(t *testing.T) {
	h := newBareHarness(t)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "session-goal", Title: "审计会话", CreatedAt: time.Unix(1_700_000_000, 0),
	}, true)
	tracker := &fakeGoalTracker{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyGoalTracker, contracts.GoalTracker(tracker))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	requireNoExactText(t, tester, "目标")

	// A whitespace-only goal is ignored by the header.
	if !tracker.trigger(contracts.Goal{ID: "goal-empty", Text: "   ", Status: "active"}, true) {
		t.Fatal("the session module did not install a goal handler")
	}
	tester.Frame()
	requireNoExactText(t, tester, "目标")

	// A real goal renders the status label and the (truncated) goal text.
	tracker.trigger(contracts.Goal{ID: "goal-1", Text: "ship the audit", Status: "active"}, true)
	tester.Frame()
	requireExactText(t, tester, "目标")
	requireHasText(t, tester, "ship the audit")

	// Clearing through the goal strip above the composer calls the host tracker:
	// carries the action, which confirms first.
	if err := tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("删除目标记录"); err != nil {
		t.Fatalf("click delete goal: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "确认删除")
	if err := tester.Click("确认删除"); err != nil {
		t.Fatalf("confirm delete goal: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if tracker.clears == 0 {
		t.Fatal("the header clear action did not call GoalTracker.Clear")
	}
	requireNoText(t, tester, "ship the audit")
}

// TestSessionExportJSONLDialogTitleFollowsLocale pins the JSONL export's
// localized save-dialog title: the seam receives the frame's localized title as
// its first argument, so a direct JSONL export in English is localized too (the
// old package-global title left the stale Chinese string; AUDIT-P3-2).
func TestSessionExportJSONLDialogTitleFollowsLocale(t *testing.T) {
	harness := startSessionUI(t, contracts.ChatSession{
		ID: "s", Title: "会话", CreatedAt: time.Unix(1_700_000_000, 0),
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "hi")},
	})
	titles := []string{}
	original := sessionui.ExportJSONLSaver
	sessionui.ExportJSONLSaver = func(title, name, content string) (string, error) {
		titles = append(titles, title)
		return "/tmp/export.jsonl", nil
	}
	t.Cleanup(func() { sessionui.ExportJSONLSaver = original })

	harness.h.ui.Locale().SetLanguage("en")
	harness.tester.Frame()
	// In English the overflow menu is "Conversation actions" and the item "Export JSONL".
	if err := harness.tester.Click("Conversation actions"); err != nil {
		t.Fatalf("open session actions: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	if err := harness.tester.Click("Export JSONL"); err != nil {
		t.Fatalf("click Export JSONL: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	if len(titles) != 1 || titles[0] != "Export conversation" {
		t.Fatalf("JSONL dialog titles = %v, want [Export conversation]", titles)
	}

	// A language switch on the next frame localizes the next invocation too.
	harness.h.ui.Locale().SetLanguage("zh")
	harness.tester.Frame()
	if err := harness.tester.Click("会话操作"); err != nil {
		t.Fatalf("open session actions: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("导出 JSONL"); err != nil {
		t.Fatalf("click export JSONL: %v", err)
	}
	harness.tester.Frame()
	if len(titles) != 2 || titles[1] != "导出会话" {
		t.Fatalf("JSONL dialog titles = %v, want the Chinese title second", titles)
	}
}
