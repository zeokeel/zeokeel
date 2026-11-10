package regression

// Adversarial audit: the session export toast wiring (optional host Notifier).
// The frame must raise exactly one toast per export outcome with the documented
// level/copy, raise none on cancel, and keep working when no notifier exists.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// sessionToastHarness is the conversation screen plus its capture notifier.
type sessionToastHarness struct {
	*sessionHarness
	notifier *fakeNotifier
}

// startSessionUIWithNotifier boots the session screen with a capture notifier.
func startSessionUIWithNotifier(t *testing.T, notifier contracts.Notifier) *sessionToastHarness {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-toast", Title: "导出会话",
		CreatedAt: time.Unix(1_700_000_000, 0),
		Messages:  []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	if notifier != nil {
		provide(t, h.ctx, contracts.KeyNotifier, notifier)
	}
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	base := &sessionHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	base.tester.Frame()
	capture, _ := notifier.(*fakeNotifier)
	return &sessionToastHarness{sessionHarness: base, notifier: capture}
}

// installExportSeams replaces both export seams for one test.
func installExportSeams(t *testing.T, markdown, jsonl func(title, name, content string) (string, error)) {
	t.Helper()
	originalMarkdown := sessionui.ExportSaver
	originalJSONL := sessionui.ExportJSONLSaver
	sessionui.ExportSaver = markdown
	sessionui.ExportJSONLSaver = jsonl
	t.Cleanup(func() {
		sessionui.ExportSaver = originalMarkdown
		sessionui.ExportJSONLSaver = originalJSONL
	})
}

// clickExport opens the header overflow menu and clicks one export item.
func clickExport(t *testing.T, harness *sessionToastHarness, item string) {
	t.Helper()
	// The overflow menu button is localized (更多 / More).
	menu := "会话操作"
	if err := harness.tester.Click(menu); err != nil {
		menu = "Conversation actions"
		if err := harness.tester.Click(menu); err != nil {
			t.Fatalf("open session actions: %v; texts = %v", err, harness.tester.Texts())
		}
	}
	harness.tester.Frame()
	if err := harness.tester.Click(item); err != nil {
		t.Fatalf("click %q: %v; texts = %v", item, err, harness.tester.Texts())
	}
	harness.tester.Frame()
}

// TestSessionExportNotifySuccessParameters covers success: exactly one toast
// with the documented success level and localized copy (zh and en), for both the
// Markdown and JSONL exports.
func TestSessionExportNotifySuccessParameters(t *testing.T) {
	success := func(title, name, content string) (string, error) { return "/tmp/" + name, nil }

	for _, testCase := range []struct {
		name  string
		item  string
		setup func(t *testing.T, harness *sessionToastHarness)
	}{
		{name: "markdown zh", item: "导出 Markdown", setup: func(*testing.T, *sessionToastHarness) {}},
		{name: "jsonl zh", item: "导出 JSONL", setup: func(*testing.T, *sessionToastHarness) {}},
		{name: "markdown en", item: "Export Markdown", setup: func(t *testing.T, harness *sessionToastHarness) {
			harness.h.ui.Locale().SetLanguage("en")
			harness.tester.Frame()
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			notifier := &fakeNotifier{}
			harness := startSessionUIWithNotifier(t, notifier)
			testCase.setup(t, harness)
			installExportSeams(t, success, success)
			clickExport(t, harness, testCase.item)

			delivered := notifier.delivered
			if len(delivered) != 1 {
				t.Fatalf("notifications = %+v, want exactly one export toast", delivered)
			}
			wantText := "对话已导出"
			if strings.Contains(testCase.name, " en") {
				wantText = "Conversation exported"
			}
			if delivered[0].Level != contracts.NotifySuccess || delivered[0].Text != wantText {
				t.Fatalf("toast = %+v, want level success and text %q", delivered[0], wantText)
			}
		})
	}
}

// TestSessionExportNotifyErrorParameters covers failure: exactly one error toast
// carrying the localized prefix plus the underlying error, once.
func TestSessionExportNotifyErrorParameters(t *testing.T) {
	failure := func(title, name, content string) (string, error) { return "", errors.New("disk full") }
	for _, item := range []string{"导出 Markdown", "导出 JSONL"} {
		t.Run(item, func(t *testing.T) {
			notifier := &fakeNotifier{}
			harness := startSessionUIWithNotifier(t, notifier)
			installExportSeams(t, failure, failure)
			clickExport(t, harness, item)

			delivered := notifier.delivered
			if len(delivered) != 1 {
				t.Fatalf("notifications = %+v, want exactly one error toast", delivered)
			}
			toast := delivered[0]
			if toast.Level != contracts.NotifyError {
				t.Fatalf("toast level = %q, want error", toast.Level)
			}
			if !strings.Contains(toast.Text, "disk full") {
				t.Fatalf("toast text = %q, want the underlying error", toast.Text)
			}
			if count := strings.Count(toast.Text, "导出失败"); count != 1 {
				t.Fatalf("toast text = %q, has %d 导出失败 prefixes, want exactly 1", toast.Text, count)
			}
		})
	}
}

// TestSessionExportNotifyCancelAndAbsentNotifier covers the two quiet paths: a
// cancelled save raises no toast, and an export without any notifier still works.
func TestSessionExportNotifyCancelAndAbsentNotifier(t *testing.T) {
	cancel := func(title, name, content string) (string, error) { return "", nil }
	notifier := &fakeNotifier{}
	cancelHarness := startSessionUIWithNotifier(t, notifier)
	installExportSeams(t, cancel, cancel)
	clickExport(t, cancelHarness, "导出 Markdown")
	if delivered := notifier.delivered; len(delivered) != 0 {
		t.Fatalf("a cancelled export raised toasts: %+v", delivered)
	}
	requireNoText(t, cancelHarness.tester, "导出失败")

	// No notifier: the export still succeeds quietly (no resident badge, no
	// error line) and nothing panics.
	noNotifier := startSessionUIWithNotifier(t, nil)
	installExportSeams(t, func(title, name, content string) (string, error) { return "/tmp/" + name, nil }, cancel)
	clickExport(t, noNotifier, "导出 Markdown")
	requireNoText(t, noNotifier.tester, "导出失败")
}
