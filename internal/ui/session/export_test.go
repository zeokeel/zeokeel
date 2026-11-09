package session_test

// Tests for the session export action: the ExportSaver seam, the Markdown
// content/default file name, the success badge and the cancel/error paths.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
)

// setExportSaver replaces the package export seam for one test.
func setExportSaver(t *testing.T, saver func(title, defaultName, content string) (string, error)) {
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
	setExportSaver(t, func(title, defaultName, content string) (string, error) {
		gotName, gotContent = defaultName, content
		return "/tmp/导出/项目讨论.md", nil
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "更多")
	h.click(tester, "更多")
	h.click(tester, "导出 Markdown")

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
	// Success is a transient toast, never a resident header badge.
	h.assertNoTexts(tester, "已导出")
}

func TestExportSanitizesDefaultName(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: `项目/讨论: 2026 *?`,
	}, true)
	var gotName string
	setExportSaver(t, func(title, defaultName, _ string) (string, error) {
		gotName = defaultName
		return "/tmp/out.md", nil
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.click(tester, "更多")
	h.click(tester, "导出 Markdown")

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
		setExportSaver(t, func(string, string, string) (string, error) { return "", nil })
		h := newHarness(t, chat)
		tester := h.tester()
		h.click(tester, "更多")
		h.click(tester, "导出 Markdown")
		h.assertNoTexts(tester, "已导出", "导出失败")
	})

	t.Run("error", func(t *testing.T) {
		chat, _ := exportChat()
		setExportSaver(t, func(string, string, string) (string, error) {
			return "", errors.New("disk full")
		})
		h := newHarness(t, chat)
		tester := h.tester()
		h.click(tester, "更多")
		h.click(tester, "导出 Markdown")
		h.assertTexts(tester, "导出失败：disk full")
		h.assertNoTexts(tester, "已导出")
	})
}

func TestExportHiddenWithoutActiveSession(t *testing.T) {
	chat := newFakeChat()
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "开始新对话")
	h.assertNoTexts(tester, "更多")
}

// TestExportJSONL covers the JSONL transcript export: a session header line and
// one record per message, written through the JSONL seam.
func TestExportJSONL(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "JSONL 导出", Model: "deepseek-chat",
		CreatedAt: time.Unix(1700000000, 0), UpdatedAt: time.Unix(1700000100, 0),
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "你好！", Reasoning: "思考",
				Tools: []contracts.ToolCall{{
					ID: "c1", Name: "bash", Summary: "ls", Status: "completed",
					Arguments:  `{"command":"ls"}`,
					TextOffset: len("你好！"),
				}}},
		},
	}, true)
	h := newHarness(t, chat)
	var savedPath, savedContent string
	old := session.ExportJSONLSaver
	session.ExportJSONLSaver = func(title, name, content string) (string, error) {
		savedPath = name
		savedContent = content
		return "/tmp/" + name, nil
	}
	t.Cleanup(func() { session.ExportJSONLSaver = old })
	tester := h.tester()
	h.click(tester, "更多")
	h.click(tester, "导出 JSONL")
	tester.Frame()
	if !strings.HasSuffix(savedPath, ".jsonl") {
		t.Fatalf("export name = %q", savedPath)
	}
	lines := strings.Split(strings.TrimSpace(savedContent), "\n")
	if len(lines) != 3 {
		t.Fatalf("jsonl lines = %d, want 3", len(lines))
	}
	var header, first, second map[string]any
	for index, target := range []*map[string]any{&header, &first, &second} {
		if err := json.Unmarshal([]byte(lines[index]), target); err != nil {
			t.Fatalf("line %d: %v", index, err)
		}
	}
	if header["type"] != "session" || header["id"] != "sess-1" || header["model"] != "deepseek-chat" {
		t.Fatalf("header = %+v", header)
	}
	if first["role"] != "user" || first["text"] != "你好" {
		t.Fatalf("first = %+v", first)
	}
	if second["role"] != "assistant" || second["reasoning"] != "思考" {
		t.Fatalf("second = %+v", second)
	}
	if !strings.Contains(savedContent, "bash") {
		t.Fatalf("tool call missing from jsonl: %s", savedContent)
	}
	// The flattened tool record keeps its transcript offset so a reader can
	// rebuild the text/card order.
	tools, ok := second["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one flattened tool", second["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if got, _ := tool["textOffset"].(float64); int(got) != len("你好！") {
		t.Fatalf("tool textOffset = %v, want %d", tool["textOffset"], len("你好！"))
	}
	// S-20: the tool's input travels with the export, so a reader can audit
	// what the call was asked to do.
	if tool["arguments"] != `{"command":"ls"}` {
		t.Fatalf("tool arguments = %v, want the raw call arguments", tool["arguments"])
	}
	if tester.HasText("导出失败") {
		t.Fatalf("export failed; texts = %v", tester.Texts())
	}
}
