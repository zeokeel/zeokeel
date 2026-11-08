package session_test

// Tests for the conversation slash commands (new/fork/export/settings) and the
// message-level 重新生成 action.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// extendedChat adds the optional fork/regenerate halves to the session fake so
// the slash commands and the regenerate action can be exercised.
type extendedChat struct {
	*fakeChat
	mu sync.Mutex

	forks    []string
	regens   []string
	forkErr  error
	regenErr error
}

// newCommandHarness is newHarness for any contracts.ChatService implementation:
// the command tests need the fake's optional fork/regenerate halves, so they
// cannot pass the concrete *fakeChat the standard harness takes.
func newCommandHarness(t *testing.T, chat contracts.ChatService) *harness {
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
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot}
}

func newExtendedChat() *extendedChat {
	return &extendedChat{fakeChat: newFakeChat()}
}

func (c *extendedChat) ForkSession(sessionID string) (string, error) {
	c.mu.Lock()
	c.forks = append(c.forks, sessionID)
	err := c.forkErr
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return c.fakeChat.NewSession("副本")
}

func (c *extendedChat) Regenerate(sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.regens = append(c.regens, sessionID)
	return c.regenErr
}

func (c *extendedChat) calls() (forks, regens []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.forks...), append([]string(nil), c.regens...)
}

func sessionCommand(t *testing.T, h *harness, id string) contracts.CommandSpec {
	t.Helper()
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == id {
			return command
		}
	}
	t.Fatalf("command %q is not registered; commands = %v", id, h.ui.Runtime().Commands())
	return contracts.CommandSpec{}
}

// --- slash commands ---------------------------------------------------------

func TestSessionSlashCommandsRegistered(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "命令"}, true)
	h := newCommandHarness(t, chat)

	wanted := []struct {
		id, title, hint string
	}{
		{"zeokeel.session.new", "新会话", "新建并打开一个会话"},
		{"zeokeel.session.fork", "创建副本", "复制当前会话"},
		{"zeokeel.session.export", "导出对话", "导出当前会话为 Markdown"},
		{"zeokeel.session.settings", "设置", "打开设置面板"},
	}
	for _, want := range wanted {
		command := sessionCommand(t, h, want.id)
		if command.Title != want.title || command.Hint != want.hint {
			t.Fatalf("%s = %q/%q, want %q/%q", want.id, command.Title, command.Hint, want.title, want.hint)
		}
		if want.id == "zeokeel.session.fork" || want.id == "zeokeel.session.export" {
			if command.Enabled == nil || !command.Enabled() {
				t.Fatalf("%s is not enabled with an active session", want.id)
			}
		}
	}

	// Without an active session (and without the optional fork half) the
	// session-bound commands are disabled.
	empty := newHarness(t, newFakeChat())
	for _, id := range []string{"zeokeel.session.fork", "zeokeel.session.export"} {
		command := sessionCommand(t, empty, id)
		if command.Enabled == nil || command.Enabled() {
			t.Fatalf("%s is enabled without an active session", id)
		}
	}
}

func TestSessionSlashCommandActions(t *testing.T) {
	setExportSaver(t, func(defaultName, content string) (string, error) {
		return "/tmp/" + defaultName, nil
	})
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "命令", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()

	// 新会话: creates and navigates.
	h.typeSlash(tester)
	h.assertTexts(tester, "新会话", "创建副本", "导出对话", "设置")
	h.click(tester, "新会话")
	if ids := chat.newSessionIDs(); len(ids) != 1 {
		t.Fatalf("NewSession calls = %v, want one", ids)
	}
	if got := h.ui.Navigator().Current(); got != "session" {
		t.Fatalf("navigator current = %q, want session", got)
	}

	// 创建副本: forks the active session and navigates.
	before := len(chat.newSessionIDs())
	h.typeSlash(tester)
	h.click(tester, "创建副本")
	forks, _ := chat.calls()
	if len(forks) != 1 || forks[0] == "" {
		t.Fatalf("ForkSession calls = %v, want the active session", forks)
	}
	if after := len(chat.newSessionIDs()); after != before+1 {
		t.Fatalf("fork did not create a session: new sessions %d → %d", before, after)
	}
	if got := h.ui.Navigator().Current(); got != "session" {
		t.Fatalf("navigator current = %q after fork, want session", got)
	}

	// 导出对话: writes the active session through the export seam.
	h.typeSlash(tester)
	h.click(tester, "导出对话")
	h.assertNoTexts(tester, "导出失败")

	// 设置: toggles the settings surface (the palette de-duplicates it against
	// the home module's command of the same title).
	h.typeSlash(tester)
	h.click(tester, "设置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("settings command did not open the settings surface")
	}
}

func TestSessionSlashCommandExportSeam(t *testing.T) {
	var names []string
	setExportSaver(t, func(defaultName, content string) (string, error) {
		names = append(names, defaultName)
		return "/tmp/out.md", nil
	})
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "导出", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
	}}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	h.typeSlash(tester)
	h.click(tester, "导出对话")
	if len(names) != 1 || !strings.HasSuffix(names[0], ".md") {
		t.Fatalf("ExportSaver names = %v, want one .md file", names)
	}
}

// --- regenerate -------------------------------------------------------------

func TestRegenerateActionShownAndCallsBackend(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "重生成", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "重新生成")

	h.click(tester, "重新生成")
	_, regens := chat.calls()
	if len(regens) != 1 || regens[0] != "sess-1" {
		t.Fatalf("Regenerate calls = %v, want [sess-1]", regens)
	}
	h.assertNoTexts(tester, "重新生成失败")
}

func TestRegenerateHiddenWithoutRegeneratorOrWhileRunning(t *testing.T) {
	// A plain fakeChat has no SessionRegenerator half.
	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{ID: "sess-1", Title: "普通", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	h := newHarness(t, plain)
	h.assertNoTexts(h.tester(), "重新生成")

	// Streaming sessions hide the action even with a regenerator backend.
	running := newExtendedChat()
	running.addSession(contracts.ChatSession{ID: "sess-1", Title: "运行中", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: "user", Text: "你好"},
			{ID: "m2", Role: "assistant", Text: "生成中"},
		}}, true)
	h2 := newCommandHarness(t, running)
	h2.assertNoTexts(h2.tester(), "重新生成")
}

func TestRegenerateDisabledForEmptyOrFailedReply(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "空回复", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: ""},
	}}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "重新生成")
	h.click(tester, "重新生成")
	if _, regens := chat.calls(); len(regens) != 0 {
		t.Fatalf("disabled regenerate called the backend: %v", regens)
	}
}

func TestRegenerateErrorShown(t *testing.T) {
	chat := newExtendedChat()
	chat.regenErr = errors.New("backend unavailable")
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "错误", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	h.click(tester, "重新生成")
	h.assertTexts(tester, "重新生成失败：backend unavailable")
}
