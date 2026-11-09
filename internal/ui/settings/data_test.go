package settings_test

// Tests for the settings 数据与隐私 (data & privacy) page: the data directory
// (copy/open), export-all-sessions Markdown export, clearing all sessions with
// a native confirmation, the diagnostic report export, the empty-session
// fallbacks and the English copy. Written before the implementation (TDD).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// dataPageHarness opens the data & privacy page with an optional data directory
// and app info provided after activation (the page reads both per frame).
func dataPageHarness(t *testing.T, chat *fakeChat, store *fakeSettings, dataDir string, info contracts.AppInfo) *harness {
	t.Helper()
	h := newHarness(t, chat, store, settings.Config{})
	if dataDir != "" {
		provide(t, h.ctx, contracts.KeyDataDir, dataDir)
	}
	if info.Name != "" {
		provide[contracts.AppInfo](t, h.ctx, contracts.KeyAppInfo, info)
	}
	tester := h.tester()
	tester.Frame()
	h.click(tester, "数据与隐私")
	return h
}

// seedDataPageSessions adds two sessions with user/assistant messages.
func seedDataPageSessions(chat *fakeChat) {
	now := time.Now()
	chat.addSession(contracts.ChatSession{
		ID: "s1", Title: "第一次对话", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Messages: []contracts.ChatMessage{
			{Role: contracts.ChatRoleUser, Text: "你好"},
			{Role: contracts.ChatRoleAssistant, Text: "你好，有什么可以帮你？"},
		},
	})
	chat.addSession(contracts.ChatSession{
		ID: "s2", Title: "第二次对话", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Minute),
		Messages: []contracts.ChatMessage{
			{Role: contracts.ChatRoleUser, Text: "第二个问题"},
		},
	})
}

// overrideOpenPath replaces the open-in-file-manager seam for one test.
func overrideOpenPath(t *testing.T, fn func(string) error) {
	t.Helper()
	previous := settings.OpenPathFunc
	settings.OpenPathFunc = fn
	t.Cleanup(func() { settings.OpenPathFunc = previous })
}

// overrideSavers replaces both export seams for one test.
func overrideSavers(t *testing.T, sessions func(string, string, string) (string, error), report func(string, string, string) (string, error)) {
	t.Helper()
	prevSessions, prevReport := settings.ExportSessionsSaver, settings.ExportReportSaver
	if sessions != nil {
		settings.ExportSessionsSaver = sessions
	}
	if report != nil {
		settings.ExportReportSaver = report
	}
	t.Cleanup(func() {
		settings.ExportSessionsSaver = prevSessions
		settings.ExportReportSaver = prevReport
	})
}

func TestDataPrivacyPageRenders(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	store := newFakeSettings(nil)
	h := dataPageHarness(t, chat, store, "/data/zeokeel-test", contracts.AppInfo{Name: "ZeoKeel", Version: "1.2.3"})
	tester := h.tester()

	h.assertTexts(tester, "数据与隐私", "数据目录", "/data/zeokeel-test",
		"复制", "打开数据目录", "会话数据", "导出全部会话", "清除全部会话",
		"诊断报告", "导出诊断报告")

	h.click(tester, "复制")
	if got := tester.Clipboard(); got != "/data/zeokeel-test" {
		t.Fatalf("clipboard = %q, want the data directory", got)
	}
	h.assertTexts(tester, "已复制")
}

func TestDataPrivacyOpenDirectory(t *testing.T) {
	var opened []string
	overrideOpenPath(t, func(path string) error {
		opened = append(opened, path)
		return nil
	})
	chat := &fakeChat{}
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/open-me", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "打开数据目录")
	if len(opened) != 1 || opened[0] != "/data/open-me" {
		t.Fatalf("opened paths = %v, want [/data/open-me]", opened)
	}

	overrideOpenPath(t, func(string) error { return errors.New("no file manager") })
	h.click(tester, "打开数据目录")
	h.assertTexts(tester, "打开数据目录失败：no file manager")
}

func TestDataPrivacyExportAllSessions(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	var gotTitle, gotName, gotContent string
	overrideSavers(t, func(title, defaultName, content string) (string, error) {
		gotTitle, gotName, gotContent = title, defaultName, content
		return "/tmp/exported-sessions.md", nil
	}, nil)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/export", contracts.AppInfo{})
	tester := h.tester()

	h.click(tester, "导出全部会话")
	if gotTitle != "导出全部会话" {
		t.Fatalf("export dialog title = %q, want the localized 导出全部会话", gotTitle)
	}
	if !strings.HasPrefix(gotName, "zeokeel-sessions-") || !strings.HasSuffix(gotName, ".md") {
		t.Fatalf("export default name = %q, want zeokeel-sessions-<date>.md", gotName)
	}
	for _, want := range []string{"# 第一次对话", "# 第二次对话", "**user**", "**assistant**", "你好", "第二个问题"} {
		if !strings.Contains(gotContent, want) {
			t.Fatalf("export content missing %q:\n%s", want, gotContent)
		}
	}
	h.assertTexts(tester, "已导出 2 个会话", "/tmp/exported-sessions.md")
}

func TestDataPrivacyExportCanceled(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	overrideSavers(t, func(string, string, string) (string, error) { return "", nil }, nil)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/export", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出全部会话")
	h.assertNoTexts(tester, "已导出", "导出会话失败")
}

func TestDataPrivacyExportError(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	overrideSavers(t, func(string, string, string) (string, error) {
		return "", errors.New("disk full")
	}, nil)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/export", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出全部会话")
	h.assertTexts(tester, "导出会话失败：disk full")
}

func TestDataPrivacyClearAllSessions(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/clear", contracts.AppInfo{})
	tester := h.tester()

	h.click(tester, "清除全部会话")
	h.assertTexts(tester, "将删除全部 2 个会话", "取消", "确认清除")
	h.click(tester, "确认清除")

	deleted := chat.deletions()
	if len(deleted) != 2 {
		t.Fatalf("DeleteSession calls = %v, want both sessions", deleted)
	}
	h.assertTexts(tester, "已清除 2 个会话")
	if sessions := chat.Sessions(); len(sessions) != 0 {
		t.Fatalf("sessions after clear = %+v, want none", sessions)
	}
}

func TestDataPrivacyClearCanceled(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/clear", contracts.AppInfo{})
	tester := h.tester()

	h.click(tester, "清除全部会话")
	h.click(tester, "取消")
	if deleted := chat.deletions(); len(deleted) != 0 {
		t.Fatalf("DeleteSession calls after cancel = %v, want none", deleted)
	}
	if sessions := chat.Sessions(); len(sessions) != 2 {
		t.Fatalf("sessions after cancel = %d, want 2", len(sessions))
	}
}

func TestDataPrivacyClearFailures(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	chat.deleteErr = errors.New("locked")
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/clear", contracts.AppInfo{})
	tester := h.tester()

	h.click(tester, "清除全部会话")
	h.click(tester, "确认清除")
	h.assertTexts(tester, "已清除 0 个会话，2 个失败")
}

func TestDataPrivacyDiagnosticReport(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:      "deepseek-chat",
		contracts.SettingsChatAPIKey:     "sk-secret",
		contracts.SettingWebSearchAPIKey: "search-secret",
		contracts.SettingMCPServers:      `[{"name":"mcp1","url":"https://mcp.example/mcp","headers":["Authorization: Bearer mcp-token-123"]}]`,
		"ui.language":                    "zh",
	})
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	var gotTitle, gotName, gotContent string
	overrideSavers(t, nil, func(title, defaultName, content string) (string, error) {
		gotTitle, gotName, gotContent = title, defaultName, content
		return "/tmp/diagnostics.txt", nil
	})
	h := dataPageHarness(t, chat, store, "/data/report", contracts.AppInfo{
		Name: "ZeoKeel", Version: "1.2.3", Commit: "abc123", BuildTime: "2026-11-08",
	})
	tester := h.tester()

	h.click(tester, "导出诊断报告")
	if gotTitle != "诊断报告" {
		t.Fatalf("report dialog title = %q, want 诊断报告", gotTitle)
	}
	if !strings.HasPrefix(gotName, "zeokeel-diagnostics-") || !strings.HasSuffix(gotName, ".txt") {
		t.Fatalf("report default name = %q, want zeokeel-diagnostics-<date>.txt", gotName)
	}
	for _, want := range []string{"ZeoKeel", "1.2.3", "abc123", "/data/report", "会话数: 2",
		"chat.model=deepseek-chat", "已设置(隐藏)"} {
		if !strings.Contains(gotContent, want) {
			t.Fatalf("diagnostic report missing %q:\n%s", want, gotContent)
		}
	}
	for _, leaked := range []string{"sk-secret", "search-secret", "mcp-token-123"} {
		if strings.Contains(gotContent, leaked) {
			t.Fatalf("diagnostic report leaked %q:\n%s", leaked, gotContent)
		}
	}
	for _, want := range []string{"web.search.apiKey=已设置(隐藏)", "Bearer ***"} {
		if !strings.Contains(gotContent, want) {
			t.Fatalf("diagnostic report missing masked form %q:\n%s", want, gotContent)
		}
	}
	h.assertTexts(tester, "已导出诊断报告", "/tmp/diagnostics.txt")
}

func TestDataPrivacyReportError(t *testing.T) {
	chat := &fakeChat{}
	overrideSavers(t, nil, func(string, string, string) (string, error) {
		return "", errors.New("permission denied")
	})
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/report", contracts.AppInfo{})
	tester := h.tester()
	h.click(tester, "导出诊断报告")
	h.assertTexts(tester, "导出诊断报告失败：permission denied")
}

func TestDataPrivacyWithoutSessions(t *testing.T) {
	chat := &fakeChat{}
	h := dataPageHarness(t, chat, newFakeSettings(nil), "/data/empty", contracts.AppInfo{})
	tester := h.tester()

	h.assertTexts(tester, "没有可导出或清除的会话")
	h.assertNoTexts(tester, "导出全部会话", "清除全部会话")
}

func TestDataPrivacyEnglish(t *testing.T) {
	chat := &fakeChat{}
	seedDataPageSessions(chat)
	overrideSavers(t, func(string, string, string) (string, error) { return "/tmp/en.md", nil }, nil)
	h := newLocaleHarness(t, chat, newFakeSettings(nil), settings.Config{}, "en")
	provide(t, h.ctx, contracts.KeyDataDir, "/data/en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Data & privacy")

	h.assertTexts(tester, "Data & privacy", "Data directory", "/data/en", "Copy",
		"Open data directory", "Session data", "Export all sessions", "Clear all sessions",
		"Diagnostic report", "Export diagnostic report", "2 sessions")
	h.click(tester, "Export all sessions")
	h.assertTexts(tester, "Exported 2 sessions", "/tmp/en.md")

	h.click(tester, "Clear all sessions")
	h.assertTexts(tester, "This deletes all 2 sessions", "Cancel", "Confirm clear")
	if got := chat.deletions(); len(got) != 0 {
		t.Fatalf("DeleteSession calls before confirming = %v, want none", got)
	}
}

var _ = context.Background
