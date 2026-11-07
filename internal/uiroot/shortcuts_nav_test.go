package uiroot

// Tests for the built-in window shortcuts Cmd/Ctrl+N (new session) and
// Cmd/Ctrl+, (settings).

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// shortcutChat is a minimal ChatService recording NewSession calls.
type shortcutChat struct {
	mu       sync.Mutex
	newCalls int
	panicNew bool
}

func (c *shortcutChat) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (c *shortcutChat) Sessions() []contracts.ChatSessionInfo { return nil }
func (c *shortcutChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (c *shortcutChat) ActiveSession() string         { return "" }
func (c *shortcutChat) SetActiveSession(string) error { return nil }
func (c *shortcutChat) NewSession(string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newCalls++
	if c.panicNew {
		panic("new session boom")
	}
	return "sess-shortcut", nil
}
func (c *shortcutChat) DeleteSession(string) error { return nil }
func (c *shortcutChat) Send(string, string) error  { return nil }
func (c *shortcutChat) Cancel(string) error        { return nil }
func (c *shortcutChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

func shortcutRoot(t *testing.T, chat contracts.ChatService) (*Root, *ui.Tester) {
	t.Helper()
	root := New(Config{AppName: "ShortcutTest", Chat: chat})
	mustScreen(t, root, textScreen("session", "会话", "session-body", 20))
	tester := newTestTester(t, root, 1000, 700)
	tester.Frame()
	return root, tester
}

func TestShortcutCmdNNewSession(t *testing.T) {
	chat := &shortcutChat{}
	root, tester := shortcutRoot(t, chat)
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	chat.mu.Lock()
	calls := chat.newCalls
	chat.mu.Unlock()
	if calls != 1 {
		t.Fatalf("NewSession calls = %d, want 1", calls)
	}
	if got := root.Navigator().Current(); got != "session" {
		t.Fatalf("navigator current = %q, want session", got)
	}
}

func TestShortcutCmdNFallsBackToRegisteredCommand(t *testing.T) {
	root := New(Config{AppName: "ShortcutTest"})
	mustScreen(t, root, textScreen("session", "会话", "session-body", 20))
	ran := 0
	unregister, err := root.Runtime().RegisterCommand(contracts.CommandSpec{
		ID: "zeokeel.session.new", Title: "新会话", Run: func() error {
			ran++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}
	t.Cleanup(unregister)
	tester := newTestTester(t, root, 1000, 700)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	if ran != 1 {
		t.Fatalf("new-session command runs = %d, want 1", ran)
	}
}

func TestShortcutCmdNWithoutChatIsSafe(t *testing.T) {
	root := New(Config{AppName: "ShortcutTest"})
	tester := newTestTester(t, root, 1000, 700)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
}

func TestShortcutCmdNSurvivesPanickingChat(t *testing.T) {
	chat := &shortcutChat{panicNew: true}
	_, tester := shortcutRoot(t, chat)
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	chat.mu.Lock()
	calls := chat.newCalls
	chat.mu.Unlock()
	if calls != 1 {
		t.Fatalf("NewSession calls = %d, want the attempted panic call", calls)
	}
}

func TestShortcutCmdCommaOpensSettings(t *testing.T) {
	root, tester := shortcutRoot(t, nil)
	if root.Navigator().SettingsOpen() {
		t.Fatal("settings already open")
	}
	tester.Key(ui.Cmd, ui.KeyComma)
	tester.Frame()
	if !root.Navigator().SettingsOpen() {
		t.Fatal("Cmd/Ctrl+, did not open settings")
	}
}

func TestShortcutCmdNDoesNotOpenPalette(t *testing.T) {
	chat := &shortcutChat{}
	root, tester := shortcutRoot(t, chat)
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	if root.paletteOpen {
		t.Fatal("Cmd/Ctrl+N opened the command palette")
	}
	// Cmd+K still opens the palette after the new shortcuts are registered.
	tester.Key(ui.Cmd, ui.KeyK)
	tester.Frame()
	if !root.paletteOpen {
		t.Fatal("Cmd/Ctrl+K did not open the palette")
	}
}

func TestShortcutCmdNResolvesChatFromKernelContext(t *testing.T) {
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	chat := &shortcutChat{}
	if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(chat)); err != nil {
		t.Fatalf("provide chat: %v", err)
	}
	root := New(Config{AppName: "ShortcutTest"})
	root.SetKernelContext(kernel)
	mustScreen(t, root, textScreen("session", "会话", "session-body", 20))
	tester := newTestTester(t, root, 1000, 700)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	chat.mu.Lock()
	calls := chat.newCalls
	chat.mu.Unlock()
	if calls != 1 {
		t.Fatalf("NewSession calls = %d, want 1 from the kernel KeyChat", calls)
	}
	if got := root.Navigator().Current(); got != "session" {
		t.Fatalf("navigator current = %q, want session", got)
	}
}
