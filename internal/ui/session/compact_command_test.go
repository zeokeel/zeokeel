package session_test

// Tests for the /compact slash command and manual context compaction feedback.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// compactChat adds contracts.SessionCompactor to the session fake.
type compactChat struct {
	*fakeChat
	mu         sync.Mutex
	calls      int
	result     contracts.CompactResult
	compactErr error
	release    chan struct{}
}

func newCompactChat() *compactChat { return &compactChat{fakeChat: newFakeChat()} }

func (c *compactChat) CompactNow(context.Context, string) (contracts.CompactResult, error) {
	c.mu.Lock()
	c.calls++
	release := c.release
	result, err := c.result, c.compactErr
	c.mu.Unlock()
	if release != nil {
		<-release
	}
	return result, err
}

func (c *compactChat) callsCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func compactSessionChat() *compactChat {
	chat := newCompactChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "压缩会话",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "你好呀"},
		},
	}, true)
	return chat
}

func findCompactCommand(t *testing.T, h *harness) contracts.CommandSpec {
	t.Helper()
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == "zeokeel.session.compact" {
			return command
		}
	}
	t.Fatalf("compact command not registered; commands = %v", h.ui.Runtime().Commands())
	return contracts.CommandSpec{}
}

func waitCompactNotice(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		tester.Frame()
		if tester.HasText("已压缩") || tester.HasText("Compacted") ||
			tester.HasText("压缩失败") || tester.HasText("Compaction failed") ||
			tester.HasText("会话正在运行") || tester.HasText("没有可压缩") ||
			tester.HasText("保存压缩结果失败") || tester.HasText("压缩不可用") {
			return
		}
	}
	t.Fatalf("compact feedback missing; texts = %v", tester.Texts())
}

func TestCompactCommandRegisteredAndEnabled(t *testing.T) {
	chat := compactSessionChat()
	h := newModelHarness(t, chat, nil)
	command := findCompactCommand(t, h)
	if command.Title != "压缩上下文" || command.Order != 47 || command.Hint == "" {
		t.Fatalf("compact command = %+v", command)
	}
	if command.Enabled == nil || !command.Enabled() {
		t.Fatal("compact command disabled for an idle session with messages")
	}

	running := compactSessionChat()
	running.mu.Lock()
	running.sessions["sess-1"] = contracts.ChatSession{ID: "sess-1", Title: "运行中", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "x"}}}
	running.mu.Unlock()
	h = newModelHarness(t, running, nil)
	if command := findCompactCommand(t, h); command.Enabled != nil && command.Enabled() {
		t.Fatal("compact command enabled while running")
	}

	empty := compactSessionChat()
	empty.mu.Lock()
	empty.sessions["sess-1"] = contracts.ChatSession{ID: "sess-1", Title: "空"}
	empty.mu.Unlock()
	h = newModelHarness(t, empty, nil)
	if command := findCompactCommand(t, h); command.Enabled != nil && command.Enabled() {
		t.Fatal("compact command enabled without messages")
	}

	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{ID: "sess-1", Title: "普通",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "x"}}}, true)
	h = newModelHarness(t, plain, nil)
	if command := findCompactCommand(t, h); command.Enabled != nil && command.Enabled() {
		t.Fatal("compact command enabled without a SessionCompactor backend")
	}
}

func TestCompactCommandSuccessBadge(t *testing.T) {
	chat := compactSessionChat()
	chat.result = contracts.CompactResult{Replaced: 8, Saved: 3, BeforeTokens: 1000, AfterTokens: 420}
	notifier := &fakeSessionNotifier{}
	h := newCompactNotifierHarness(t, chat, notifier)
	tester := h.tester()
	command := findCompactCommand(t, h)
	if err := command.Run(); err != nil {
		t.Fatalf("compact Run: %v", err)
	}
	if !waitCompactToast(t, h, tester, notifier, "已压缩：8 条 → 3 条") {
		t.Fatalf("compact success toast missing; texts = %v", tester.Texts())
	}
	// The completed compaction never becomes a resident badge.
	h.assertNoTexts(tester, "已压缩")
	if got := chat.callsCount(); got != 1 {
		t.Fatalf("CompactNow calls = %d", got)
	}
}

// TestCompactCommandShowsLoading covers the in-flight compaction state: the
// composer shows 压缩中… while the backend works, and the result goes to a toast.
func TestCompactCommandShowsLoading(t *testing.T) {
	chat := compactSessionChat()
	chat.release = make(chan struct{})
	notifier := &fakeSessionNotifier{}
	h := newCompactNotifierHarness(t, chat, notifier)
	tester := h.tester()
	if err := findCompactCommand(t, h).Run(); err != nil {
		t.Fatalf("compact Run: %v", err)
	}
	tester.Frame()
	if !tester.HasText("压缩中…") {
		t.Fatalf("in-flight compaction has no loading state; texts = %v", tester.Texts())
	}
	close(chat.release)
	if !waitCompactToast(t, h, tester, notifier, "已压缩") {
		t.Fatalf("compaction result toast missing; texts = %v", tester.Texts())
	}
}

func TestCompactCommandErrorMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"running", contracts.ErrCompactRunning, "会话正在运行，无法压缩"},
		{"unavailable", contracts.ErrCompactUnavailable, "压缩不可用"},
		{"nothing", contracts.ErrCompactNothing, "没有可压缩的内容"},
		{"save", contracts.ErrCompactSave, "保存压缩结果失败"},
		{"other", errors.New("boom"), "压缩失败：boom"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chat := compactSessionChat()
			chat.compactErr = test.err
			h := newModelHarness(t, chat, nil)
			tester := h.tester()
			if err := findCompactCommand(t, h).Run(); err != nil {
				t.Fatalf("compact Run: %v", err)
			}
			waitCompactNotice(t, h, tester)
			h.assertTexts(tester, test.want)
		})
	}
}

func TestCompactCommandPreventsDuplicates(t *testing.T) {
	chat := compactSessionChat()
	chat.release = make(chan struct{})
	h := newModelHarness(t, chat, nil)
	command := findCompactCommand(t, h)
	if err := command.Run(); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	for attempt := 0; attempt < 2000 && chat.callsCount() == 0; attempt++ {
		time.Sleep(time.Millisecond)
	}
	if err := command.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := chat.callsCount(); got != 1 {
		t.Fatalf("concurrent CompactNow calls = %d, want 1", got)
	}
	close(chat.release)
}

func TestCompactCommandEnglish(t *testing.T) {
	chat := compactSessionChat()
	chat.result = contracts.CompactResult{Replaced: 4, Saved: 2}
	notifier := &fakeSessionNotifier{}
	h := newCompactNotifierHarness(t, chat, notifier)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	if err := findCompactCommand(t, h).Run(); err != nil {
		t.Fatalf("compact Run: %v", err)
	}
	if !waitCompactToast(t, h, tester, notifier, "Compacted: 4 → 2 messages") {
		t.Fatalf("English compaction toast missing; texts = %v", tester.Texts())
	}
}

// newCompactNotifierHarness is newModelHarness plus the optional host notifier:
// compaction results are transient toasts whose handler records them.
func newCompactNotifierHarness(t *testing.T, chat contracts.ChatService, notifier contracts.Notifier) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot}
}

// waitCompactToast advances frames until the notifier recorded a toast
// containing fragment, and reports whether it appeared.
func waitCompactToast(t *testing.T, h *harness, tester *ui.Tester, notifier *fakeSessionNotifier, fragment string) bool {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		tester.Frame()
		for _, toast := range notifier.toasts() {
			if strings.Contains(toast.text, fragment) {
				return true
			}
		}
	}
	return false
}
