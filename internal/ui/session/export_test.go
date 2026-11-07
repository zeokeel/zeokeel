package session_test

// Tests for the session export action: the ExportSaver seam, the Markdown
// content/default file name, the success badge and the cancel/error paths.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
)

// setExportSaver replaces the package export seam for one test.
func setExportSaver(t *testing.T, saver func(defaultName, content string) (string, error)) {
	t.Helper()
	previous := session.ExportSaver
	session.ExportSaver = saver
	t.Cleanup(func() { session.ExportSaver = previous })
}

func exportChat() (*fakeChat, *contracts.ChatSession) {
	chatSession := &contracts.ChatSession{
		ID:        "sess-1",
		Title:     "项目讨论",
		CreatedAt: time.Unix(1700000000, 0),
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "帮我看看这次改动"),
			{
				ID:   "m2",
				Role: contracts.ChatRoleAssistant,
				Text: "好的，已经检查完成。",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "read_file", Status: "ok", Summary: "main.go"},
				},
			},
		},
	}
	chat := newFakeChat()
	chat.addSession(*chatSession, true)
	return chat, chatSession
}

func TestExportWritesMarkdown(t *testing.T) {
	chat, _ := exportChat()
	var gotName, gotContent string
	setExportSaver(t, func(defaultName, content string) (string, error) {
		gotName, gotContent = defaultName, content
		return "/tmp/导出/项目讨论.md", nil
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "导出")
	h.click(tester, "导出")

	if gotName != "项目讨论.md" {
		t.Fatalf("default name = %q, want 项目讨论.md", gotName)
	}
	for _, want := range []string{
		"# 项目讨论",
		"用户: 帮我看看这次改动",
		"助手: 好的，已经检查完成。",
		"[工具] read_file (ok)",
		"创建时间：",
	} {
		if !strings.Contains(gotContent, want) {
			t.Fatalf("exported content does not contain %q:\n%s", want, gotContent)
		}
	}
	h.assertTexts(tester, "已导出")
}

func TestExportSanitizesDefaultName(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: `项目/讨论: 2026 *?`,
	}, true)
	var gotName string
	setExportSaver(t, func(defaultName, _ string) (string, error) {
		gotName = defaultName
		return "/tmp/out.md", nil
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.click(tester, "导出")

	if !strings.HasSuffix(gotName, ".md") {
		t.Fatalf("default name %q has no .md suffix", gotName)
	}
	if strings.ContainsAny(gotName, `/\:*?"<>|`) {
		t.Fatalf("default name %q still contains unsafe characters", gotName)
	}
	if !strings.Contains(gotName, "项目") {
		t.Fatalf("default name %q lost the title", gotName)
	}
}

func TestExportCancelAndError(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		chat, _ := exportChat()
		setExportSaver(t, func(string, string) (string, error) { return "", nil })
		h := newHarness(t, chat)
		tester := h.tester()
		h.click(tester, "导出")
		h.assertNoTexts(tester, "已导出", "导出失败")
	})

	t.Run("error", func(t *testing.T) {
		chat, _ := exportChat()
		setExportSaver(t, func(string, string) (string, error) {
			return "", errors.New("disk full")
		})
		h := newHarness(t, chat)
		tester := h.tester()
		h.click(tester, "导出")
		h.assertTexts(tester, "导出失败：disk full")
		h.assertNoTexts(tester, "已导出")
	})
}

func TestExportHiddenWithoutActiveSession(t *testing.T) {
	chat := newFakeChat()
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "开始新对话")
	h.assertNoTexts(tester, "导出")
}
