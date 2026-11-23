package session_test

// Frame tests for the raw session-log export (DSH session-log-export): the
// conversation's ⋯ menu offers "导出原始日志（ZIP）" only when the backend
// provides contracts.SessionLogExporter; the archive goes through the
// ExportLogSaver seam (cancel stays quiet, success toasts, failures reach the
// header's exportErr line and an error toast).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeLogExporter is the optional SessionLogExporter capability under test.
type fakeLogExporter struct {
	mu      sync.Mutex
	archive []byte
	err     error
	calls   []string
}

func (f *fakeLogExporter) SessionLogArchive(_ context.Context, sessionID string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionID)
	if f.err != nil {
		return nil, f.err
	}
	return append([]byte(nil), f.archive...), nil
}

func (f *fakeLogExporter) archived() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// newLogExportHarness builds the conversation screen with an optional log
// exporter and notifier.
func newLogExportHarness(t *testing.T, chat *fakeChat, exporter contracts.SessionLogExporter,
	notifier contracts.Notifier) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, uinative.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if exporter != nil {
		provide[contracts.SessionLogExporter](t, ctx, contracts.KeySessionLogExporter, exporter)
	}
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// logExportChat is one idle conversation with a title worth archiving.
func logExportChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-log", Title: "发布检查",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "继续发布"}},
	}, true)
	return chat
}

// stubLogSaver replaces the ZIP save seam for one test.
func stubLogSaver(t *testing.T, save func(title, defaultName string, content []byte) (string, error)) *struct {
	title       string
	defaultName string
	content     []byte
	calls       int
} {
	t.Helper()
	record := &struct {
		title       string
		defaultName string
		content     []byte
		calls       int
	}{}
	previous := session.ExportLogSaver
	session.ExportLogSaver = func(title, defaultName string, content []byte) (string, error) {
		record.calls++
		record.title, record.defaultName, record.content = title, defaultName, append([]byte(nil), content...)
		return save(title, defaultName, content)
	}
	t.Cleanup(func() { session.ExportLogSaver = previous })
	return record
}

// TestConversationMenuHidesRawLogExportWithoutCapability covers the optional
// capability: without SessionLogExporter the menu has no raw-log item.
func TestConversationMenuHidesRawLogExportWithoutCapability(t *testing.T) {
	h := newLogExportHarness(t, logExportChat(), nil, nil)
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.assertNoTexts(tester, "导出原始日志（ZIP）", "Export raw log (ZIP)")
	h.assertTexts(tester, "导出 Markdown", "导出 JSONL")
}

// TestRawLogExportWritesArchiveAndToasts covers the success path: the backend
// archive reaches the ZIP save seam with the title-suffixed name and the toast
// confirms it.
func TestRawLogExportWritesArchiveAndToasts(t *testing.T) {
	exporter := &fakeLogExporter{archive: []byte("PK\x03\x04archive-bytes")}
	notifier := &fakeSessionNotifier{}
	h := newLogExportHarness(t, logExportChat(), exporter, notifier)
	record := stubLogSaver(t, func(string, string, []byte) (string, error) { return "/tmp/log.zip", nil })
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.click(tester, "导出原始日志（ZIP）")

	if calls := exporter.archived(); len(calls) != 1 || calls[0] != "sess-log" {
		t.Fatalf("SessionLogArchive calls = %v, want [sess-log]", calls)
	}
	if record.calls != 1 {
		t.Fatalf("ExportLogSaver calls = %d, want 1", record.calls)
	}
	if string(record.content) != "PK\x03\x04archive-bytes" {
		t.Fatalf("saved content = %q, want the backend archive bytes", record.content)
	}
	if record.title != "导出会话原始日志" {
		t.Fatalf("save dialog title = %q", record.title)
	}
	if !strings.HasPrefix(record.defaultName, "发布检查") || !strings.HasSuffix(record.defaultName, ".zip") {
		t.Fatalf("default name = %q, want the title + .zip", record.defaultName)
	}
	toasts := notifier.toasts()
	if len(toasts) == 0 || toasts[len(toasts)-1].text != "原始日志已导出" {
		t.Fatalf("export toasts = %+v, want 原始日志已导出", toasts)
	}
	h.assertNoTexts(tester, "导出失败")
}

// TestRawLogExportCancelStaysQuiet covers the canceled save dialog: no file, no
// toast, no error line.
func TestRawLogExportCancelStaysQuiet(t *testing.T) {
	exporter := &fakeLogExporter{archive: []byte("zip")}
	notifier := &fakeSessionNotifier{}
	h := newLogExportHarness(t, logExportChat(), exporter, notifier)
	stubLogSaver(t, func(string, string, []byte) (string, error) { return "", nil })
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.click(tester, "导出原始日志（ZIP）")
	tester.Frame()
	if toasts := notifier.toasts(); len(toasts) != 0 {
		t.Fatalf("canceled export toasts = %+v, want none", toasts)
	}
	h.assertNoTexts(tester, "导出失败", "原始日志已导出")
}

// TestRawLogExportExporterErrorShowsExportErr covers a backend failure: the
// header's error line carries it and a toast says so.
func TestRawLogExportExporterErrorShowsExportErr(t *testing.T) {
	exporter := &fakeLogExporter{err: errors.New("session log: unknown session")}
	notifier := &fakeSessionNotifier{}
	h := newLogExportHarness(t, logExportChat(), exporter, notifier)
	record := stubLogSaver(t, func(string, string, []byte) (string, error) { return "/tmp/log.zip", nil })
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.click(tester, "导出原始日志（ZIP）")
	tester.Frame()
	if record.calls != 0 {
		t.Fatalf("failed archive still reached the saver (%d calls)", record.calls)
	}
	h.assertTexts(tester, "导出失败：session log: unknown session")
	toasts := notifier.toasts()
	if len(toasts) == 0 || !strings.Contains(toasts[len(toasts)-1].text, "导出失败") {
		t.Fatalf("error toasts = %+v", toasts)
	}
}

// TestRawLogExportSaverErrorShowsExportErr covers a file-write failure.
func TestRawLogExportSaverErrorShowsExportErr(t *testing.T) {
	exporter := &fakeLogExporter{archive: []byte("zip")}
	notifier := &fakeSessionNotifier{}
	h := newLogExportHarness(t, logExportChat(), exporter, notifier)
	stubLogSaver(t, func(string, string, []byte) (string, error) { return "", errors.New("disk full") })
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.click(tester, "导出原始日志（ZIP）")
	tester.Frame()
	h.assertTexts(tester, "导出失败：disk full")
	if toasts := notifier.toasts(); len(toasts) == 0 || !strings.Contains(toasts[len(toasts)-1].text, "导出失败") {
		t.Fatalf("error toasts = %+v", toasts)
	}
}

// TestRawLogExportEnglish covers the English menu/toast/title copy.
func TestRawLogExportEnglish(t *testing.T) {
	exporter := &fakeLogExporter{archive: []byte("zip")}
	notifier := &fakeSessionNotifier{}
	h := newLogExportHarness(t, logExportChat(), exporter, notifier)
	h.ui.Locale().SetLanguage("en")
	record := stubLogSaver(t, func(string, string, []byte) (string, error) { return "/tmp/log.zip", nil })
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.assertTexts(tester, "Export raw log (ZIP)")
	h.click(tester, "Export raw log (ZIP)")
	if record.title != "Export raw session log" {
		t.Fatalf("English save title = %q", record.title)
	}
	toasts := notifier.toasts()
	if len(toasts) == 0 || toasts[len(toasts)-1].text != "Raw log exported" {
		t.Fatalf("English toasts = %+v", toasts)
	}
}
