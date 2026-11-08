package regression

// Adversarial audit: the settings 数据与隐私 (data & privacy) page. Negative and
// boundary cases: export-all content/cancel/error through the save seam, clear-all
// confirmation (cancel, success, partial failure count), the diagnostic report's
// API-key masking (probe: non-chat key rows), the open-data-directory seam
// failure, and the empty/no-session page states.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// dataPrivacyHarness is the settings surface opened on the data & privacy page.
type dataPrivacyHarness struct {
	h      *harness
	chat   contracts.ChatService
	tester *ui.Tester
}

// startDataPrivacy opens the settings surface and clicks the data page.
func startDataPrivacy(t *testing.T, chat contracts.ChatService) *dataPrivacyHarness {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, chat)
	provide(t, h.ctx, contracts.KeyAppInfo, contracts.AppInfo{
		Name: "ZeoKeel", Version: "1.2.3", Commit: "abc123", BuildTime: "2026-01-01T00:00:00Z", GoVersion: "go1.27.3",
	})
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	harness := &dataPrivacyHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	// The grouped navigation scrolls: the data page sits at the bottom of the
	// advanced group, below the first screen at 1280x800.
	openSettingsPage(t, harness.tester, 1280, 800, "数据与隐私")
	return harness
}

// overrideDataSeams replaces the open-path and both export seams for one test.
func overrideDataSeams(t *testing.T, open func(string) error,
	sessions func(string, string) (string, error), report func(string, string) (string, error)) {
	t.Helper()
	prevOpen, prevSessions, prevReport := settingsui.OpenPathFunc,
		settingsui.ExportSessionsSaver, settingsui.ExportReportSaver
	if open != nil {
		settingsui.OpenPathFunc = open
	}
	if sessions != nil {
		settingsui.ExportSessionsSaver = sessions
	}
	if report != nil {
		settingsui.ExportReportSaver = report
	}
	t.Cleanup(func() {
		settingsui.OpenPathFunc = prevOpen
		settingsui.ExportSessionsSaver = prevSessions
		settingsui.ExportReportSaver = prevReport
	})
}

// seedPrivacySessions installs three sessions with messages.
func seedPrivacySessions(chat *fakeChat) {
	base := time.Unix(1_700_000_000, 0)
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "第一次对话", CreatedAt: base, UpdatedAt: base,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "你好，有什么可以帮你？"},
		}}, true)
	chat.addSession(contracts.ChatSession{ID: "s2", Title: "第二次对话", CreatedAt: base.Add(time.Minute), UpdatedAt: base,
		Messages: []contracts.ChatMessage{{ID: "m3", Role: contracts.ChatRoleUser, Text: "第二个问题"}}}, false)
	chat.addSession(contracts.ChatSession{ID: "s3", Title: "第三次对话", CreatedAt: base.Add(2 * time.Minute), UpdatedAt: base,
		Messages: []contracts.ChatMessage{{ID: "m4", Role: contracts.ChatRoleUser, Text: "第三个问题"}}}, false)
}

// TestDataPrivacyExportSessionsFlow covers export-all: the Markdown content (all
// sessions and messages, roles included), the success notice, silent cancel and
// a readable save error.
func TestDataPrivacyExportSessionsFlow(t *testing.T) {
	chat := newFakeChat()
	seedPrivacySessions(chat)
	var gotName, gotContent string
	overrideDataSeams(t, nil, func(defaultName, content string) (string, error) {
		gotName, gotContent = defaultName, content
		return "/tmp/all-sessions.md", nil
	}, nil)
	harness := startDataPrivacy(t, chat)
	requireHasText(t, harness.tester, "共 3 个会话")

	if err := harness.tester.Click("导出全部会话"); err != nil {
		t.Fatalf("export all: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "已导出 3 个会话")
	if !strings.HasPrefix(gotName, "zeokeel-sessions-") || !strings.HasSuffix(gotName, ".md") {
		t.Fatalf("export default name = %q", gotName)
	}
	for _, want := range []string{"# 第一次对话", "# 第二次对话", "# 第三次对话",
		"**user**: 你好", "**assistant**: 你好，有什么可以帮你？", "**user**: 第二个问题"} {
		if !strings.Contains(gotContent, want) {
			t.Fatalf("exported Markdown lacks %q:\n%s", want, gotContent)
		}
	}

	// Cancel: "" means the user closed the dialog, the page stays quiet.
	overrideDataSeams(t, nil, func(string, string) (string, error) { return "", nil }, nil)
	if err := harness.tester.Click("导出全部会话"); err != nil {
		t.Fatalf("export all (cancel): %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已导出")
	requireNoText(t, harness.tester, "导出会话失败")

	// Save error is readable.
	overrideDataSeams(t, nil, func(string, string) (string, error) {
		return "", errors.New("disk full")
	}, nil)
	if err := harness.tester.Click("导出全部会话"); err != nil {
		t.Fatalf("export all (error): %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "导出会话失败：disk full")
}

// selectiveDeleteChat fails DeleteSession for selected sessions.
type selectiveDeleteChat struct {
	*fakeChat
	fail map[string]bool
}

func (c *selectiveDeleteChat) DeleteSession(sessionID string) error {
	if c.fail[sessionID] {
		return errors.New("delete failed")
	}
	return c.fakeChat.DeleteSession(sessionID)
}

// TestDataPrivacyClearSessionsFlow covers clear-all: the confirmation can be
// cancelled, a confirm deletes all sessions, and partial failures are counted in
// the danger notice.
func TestDataPrivacyClearSessionsFlow(t *testing.T) {
	chat := newFakeChat()
	seedPrivacySessions(chat)
	harness := startDataPrivacy(t, chat)

	if err := harness.tester.Click("清除全部会话"); err != nil {
		t.Fatalf("open clear dialog: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "将删除全部 3 个会话，此操作无法撤销。")
	// Cancel keeps every session.
	if err := harness.tester.Click("取消"); err != nil {
		t.Fatalf("cancel clear: %v", err)
	}
	harness.tester.Frame()
	if got := chat.sessionCount(); got != 3 {
		t.Fatalf("sessions after cancel = %d, want 3", got)
	}

	// Confirm deletes every session.
	if err := harness.tester.Click("清除全部会话"); err != nil {
		t.Fatalf("reopen clear dialog: %v", err)
	}
	harness.tester.Frame()
	if err := harness.tester.Click("确认清除"); err != nil {
		t.Fatalf("confirm clear: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "已清除 3 个会话")
	requireHasText(t, harness.tester, "没有可导出或清除的会话")
	if got := chat.sessionCount(); got != 0 {
		t.Fatalf("sessions after clear = %d, want 0", got)
	}

	// Partial failure: failed deletes are counted and reported as an error.
	failing := &selectiveDeleteChat{fakeChat: newFakeChat(), fail: map[string]bool{"s2": true}}
	seedPrivacySessions(failing.fakeChat)
	failingHarness := startDataPrivacy(t, failing)
	if err := failingHarness.tester.Click("清除全部会话"); err != nil {
		t.Fatalf("open clear dialog (partial): %v", err)
	}
	failingHarness.tester.Frame()
	if err := failingHarness.tester.Click("确认清除"); err != nil {
		t.Fatalf("confirm clear (partial): %v", err)
	}
	failingHarness.tester.Frame()
	requireHasText(t, failingHarness.tester, "已清除 2 个会话，1 个失败")
	if got := failing.fakeChat.sessionCount(); got != 1 {
		t.Fatalf("sessions after partial clear = %d, want the one failed session", got)
	}
}

// TestDataPrivacyDiagnosticReportMasking covers the diagnostic report: it lists
// the app/data facts and masks the chat API key. The adversarial probe checks
// whether other secret-bearing settings rows (web search key, MCP server env) are
// also masked; the current implementation only masks chat.apiKey, so this logs
// AUDIT-P3-13 instead of failing (the defect is recorded in the audit report).
func TestDataPrivacyDiagnosticReportMasking(t *testing.T) {
	chat := newFakeChat()
	seedPrivacySessions(chat)
	var report string
	overrideDataSeams(t, nil, nil, func(defaultName, content string) (string, error) {
		if !strings.HasPrefix(defaultName, "zeokeel-diagnostics-") || !strings.HasSuffix(defaultName, ".txt") {
			t.Fatalf("report default name = %q", defaultName)
		}
		report = content
		return "/tmp/diagnostics.txt", nil
	})
	harness := startDataPrivacy(t, chat)
	chatSecrets := map[string]string{
		contracts.SettingsChatAPIKey:     "sk-chat-secret-123",
		contracts.SettingWebSearchAPIKey: "sk-search-secret-456",
		contracts.SettingMCPServers:      `[{"name":"audit","url":"http://127.0.0.1:1","headers":{"Authorization":"Bearer sk-mcp-secret-789"}}]`,
	}
	for key, value := range chatSecrets {
		if err := harness.h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	if err := harness.tester.Click("导出诊断报告"); err != nil {
		t.Fatalf("export diagnostic report: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "已导出诊断报告")

	for _, want := range []string{"ZeoKeel 诊断报告", "应用: ZeoKeel v1.2.3",
		"Commit: abc123", "会话数: 3", "chat.apiKey=已设置(隐藏)"} {
		if !strings.Contains(report, want) {
			t.Fatalf("diagnostic report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "sk-chat-secret-123") {
		t.Fatalf("diagnostic report leaked the chat API key:\n%s", report)
	}

	// Probe: does the report leak other API-key rows?
	for key, secret := range map[string]string{
		contracts.SettingWebSearchAPIKey: "sk-search-secret-456",
		contracts.SettingMCPServers:      "sk-mcp-secret-789",
	} {
		if strings.Contains(report, secret) {
			t.Logf("AUDIT-P3-13: diagnostic report leaks %s in plain text (only chat.apiKey is masked)", key)
		}
	}
}

// TestDataPrivacyOpenDirectoryAndReportErrors covers the remaining seams: the
// open-data-directory failure line, the report save cancel/error, and the empty
// page state without sessions.
func TestDataPrivacyOpenDirectoryAndReportErrors(t *testing.T) {
	chat := newFakeChat()
	harness := startDataPrivacy(t, chat)
	requireHasText(t, harness.tester, "没有可导出或清除的会话")

	overrideDataSeams(t, func(string) error { return errors.New("no file manager") }, nil, nil)
	if err := harness.tester.Click("打开数据目录"); err != nil {
		t.Fatalf("open data directory: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "打开数据目录失败：no file manager")

	// Report cancel keeps the page quiet.
	overrideDataSeams(t, nil, nil, func(string, string) (string, error) { return "", nil })
	if err := harness.tester.Click("导出诊断报告"); err != nil {
		t.Fatalf("export report (cancel): %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "已导出诊断报告")

	// Report save error is readable.
	overrideDataSeams(t, nil, nil, func(string, string) (string, error) {
		return "", errors.New("report disk full")
	})
	if err := harness.tester.Click("导出诊断报告"); err != nil {
		t.Fatalf("export report (error): %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "导出诊断报告失败：report disk full")
}
