package session_test

// Dropped window files stage as composer attachments (the same path as the
// native picker).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type fakeFileDrop struct{ handler func(paths []string) }

func (f *fakeFileDrop) SetHandler(fn func(paths []string)) { f.handler = fn }
func (f *fakeFileDrop) Dispatch(paths []string) {
	if f.handler != nil {
		f.handler(paths)
	}
}

// newDropHarness activates the session screen with the file-drop service and an
// optional notifier, plus a second screen so a drop can land outside the
// conversation (UX-15).
func newDropHarness(t *testing.T, chat *fakeChat, notifier contracts.Notifier) (*harness, *fakeFileDrop) {
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
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	drop := &fakeFileDrop{}
	provide(t, ctx, contracts.KeyFileDrop, contracts.FileDropService(drop))
	activate(t, ctx, session.Module(), session.Config{})
	if _, err := uiRoot.Runtime().RegisterScreen(uinative.ScreenSpec{
		ID: "other-screen", Title: "其他屏", Order: 90,
		Build: func(c *ui.Context) { ui.Text(c, "其他屏幕内容") },
	}); err != nil {
		t.Fatalf("register other screen: %v", err)
	}
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}, drop
}

func TestDroppedFileStagesAttachment(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "拖放"}, true)
	h, drop := newDropHarness(t, chat, nil)

	path := filepath.Join(t.TempDir(), "dropped.txt")
	if err := os.WriteFile(path, []byte("hello drop"), 0o600); err != nil {
		t.Fatalf("write dropped file: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	drop.Dispatch([]string{path})
	// AR-10: the read runs in the background; the chip lands through the Post.
	waitHasText(t, tester, "dropped.txt", "the dropped attachment chip")
}

// TestDropOutsideSessionNotifiesAndNavigates covers UX-15: a drop while another
// screen is current stages the file AND says so — a toast with the way to the
// conversation; the action navigates there.
func TestDropOutsideSessionNotifiesAndNavigates(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "拖放"}, true)
	notifier := &fakeSessionNotifier{}
	h, drop := newDropHarness(t, chat, notifier)
	tester := h.tester()
	tester.Frame()
	if err := h.ui.Navigator().Push("other-screen"); err != nil {
		t.Fatalf("push other screen: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "其他屏幕内容")

	path := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write dropped file: %v", err)
	}
	drop.Dispatch([]string{path})

	// The toast arrives with the navigation action (the read is backgrounded).
	var action notifierAction
	found := false
	for attempt := 0; attempt < 200 && !found; attempt++ {
		tester.Frame()
		if a, ok := notifier.lastAction(); ok && a.text == "文件已暂存到会话输入框" {
			action, found = a, true
		}
	}
	if !found {
		t.Fatalf("UX-15: no staged-files toast after an off-session drop; toasts = %v", notifier.toasts())
	}
	if action.label != "前往会话" {
		t.Fatalf("toast action label = %q, want 前往会话", action.label)
	}
	// Still on the other screen: the toast offers the way, it does not yank it.
	if current := h.ui.Navigator().Current(); current != "other-screen" {
		t.Fatalf("drop navigated without asking: current = %q", current)
	}
	action.action()
	tester.Frame()
	if current := h.ui.Navigator().Current(); current != "session" {
		t.Fatalf("toast action did not open the conversation: current = %q", current)
	}
	waitHasText(t, tester, "elsewhere.txt", "the staged attachment chip after navigating")
}
