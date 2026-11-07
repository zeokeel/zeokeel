package session_test

// Regression tests (TDD RED first) for the JSONL export path: the failure line
// must carry one localized prefix (AUDIT-P3-4) and the save-dialog title must
// follow the frame locale (AUDIT-P3-2).

import (
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
)

func exportJSONLTestChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-jsonl", Title: "JSONL 导出",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	return chat
}

// withJSONLSeam replaces both the JSONL saver and the dialog titles for one
// test, restoring the package globals afterwards.
func withJSONLSeam(t *testing.T, saver func(name, content string) (string, error)) {
	t.Helper()
	oldSaver := session.ExportJSONLSaver
	oldTitle := session.ExportJSONLDialogTitle
	oldMarkdownTitle := session.ExportDialogTitle
	session.ExportJSONLSaver = saver
	t.Cleanup(func() {
		session.ExportJSONLSaver = oldSaver
		session.ExportJSONLDialogTitle = oldTitle
		session.ExportDialogTitle = oldMarkdownTitle
	})
}

func TestExportJSONLFailureShowsOnePrefix(t *testing.T) {
	withJSONLSeam(t, func(string, string) (string, error) { return "", errors.New("disk full") })
	h := newHarness(t, exportJSONLTestChat())
	tester := h.tester()

	h.click(tester, "更多")
	h.click(tester, "导出 JSONL")
	tester.Frame()

	errorText := ""
	for _, text := range tester.Texts() {
		if strings.Contains(text, "disk full") {
			errorText = text
		}
	}
	if errorText == "" {
		t.Fatalf("frame does not show the JSONL failure; texts = %v", tester.Texts())
	}
	if got := strings.Count(errorText, "导出失败"); got != 1 {
		t.Fatalf("JSONL failure line carries %d prefixes, want exactly 1: %q", got, errorText)
	}
}

func TestExportJSONLDialogTitleFollowsLocale(t *testing.T) {
	captured := ""
	withJSONLSeam(t, func(name, content string) (string, error) {
		captured = session.ExportJSONLDialogTitle
		return "/tmp/export.jsonl", nil
	})
	chat := exportJSONLTestChat()
	h := newHarness(t, chat)
	tester := h.tester()

	// Direct JSONL export in English must localize the dialog title first.
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	session.ExportJSONLDialogTitle = "导出会话"
	h.click(tester, "More")
	h.click(tester, "Export JSONL")
	tester.Frame()
	if captured != "Export conversation" {
		t.Fatalf("JSONL dialog title in English = %q, want Export conversation", captured)
	}

	// And back to Chinese on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	session.ExportJSONLDialogTitle = "Export conversation"
	h.click(tester, "更多")
	h.click(tester, "导出 JSONL")
	tester.Frame()
	if captured != "导出会话" {
		t.Fatalf("JSONL dialog title in Chinese = %q, want 导出会话", captured)
	}
}
