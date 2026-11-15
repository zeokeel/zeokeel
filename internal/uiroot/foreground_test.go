package uiroot

// Tests for the host's foreground-session read model (contracts.ForegroundSession):
// the conversation on screen in the foreground, so the agent can silence a
// background-run notification for the conversation the user is actually
// watching instead of for whatever session is merely active.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// foregroundChat is shortcutChat with a scripted active session.
type foregroundChat struct {
	shortcutChat
	active string
}

func (c *foregroundChat) ActiveSession() string { return c.active }

// foregroundRoot builds a desktop root with the conversation screen and a second
// screen (the insights surface) registered, plus its rendered frame.
func foregroundRoot(t *testing.T, chat contracts.ChatService) (*Root, *ui.Tester) {
	t.Helper()
	root := New(Config{AppName: "ForegroundTest", Chat: chat})
	mustScreen(t, root, textScreen(sessionScreenID, "会话", "session-body", 10))
	mustScreen(t, root, textScreen("insights", "洞察", "insights-body", 20))
	tester := newTestTester(t, root, 1000, 700)
	tester.Frame()
	return root, tester
}

// pushAndFrame switches to one screen and renders the frame showing it.
func pushAndFrame(t *testing.T, root *Root, tester *ui.Tester, screenID, marker string) {
	t.Helper()
	if err := root.Navigator().Push(screenID); err != nil {
		t.Fatalf("push %q: %v", screenID, err)
	}
	tester.Frame()
	if !tester.HasText(marker) {
		t.Fatalf("the %q screen did not render (%q missing); texts = %v", screenID, marker, tester.Texts())
	}
}

// TestForegroundSessionOnConversationScreen covers the positive case: while the
// conversation screen is on top, the foreground session is the active one.
func TestForegroundSessionOnConversationScreen(t *testing.T) {
	chat := &foregroundChat{active: "sess-1"}
	root, tester := foregroundRoot(t, chat)
	foreground := root.ForegroundSession()
	if foreground == nil {
		t.Fatal("the desktop root exposes no foreground-session service")
	}
	pushAndFrame(t, root, tester, sessionScreenID, "session-body")
	id, ok := foreground.ForegroundSession()
	if !ok || id != "sess-1" {
		t.Fatalf("ForegroundSession() on the conversation screen = %q, %t; want %q, true", id, ok, "sess-1")
	}
}

// TestForegroundSessionBeforeAnyScreen covers the cold root: before the first
// frame chooses a destination no conversation is on screen, so the read model
// answers false instead of guessing the backend's active session.
func TestForegroundSessionBeforeAnyScreen(t *testing.T) {
	chat := &foregroundChat{active: "sess-1"}
	root := New(Config{AppName: "ForegroundTest", Chat: chat})
	mustScreen(t, root, textScreen(sessionScreenID, "会话", "session-body", 10))
	foreground := root.ForegroundSession()
	if id, ok := foreground.ForegroundSession(); ok || id != "" {
		t.Fatalf("ForegroundSession() before the first frame = %q, %t; want %q, false", id, ok, "")
	}
}

// TestForegroundSessionOnOtherScreen covers the negative case: another screen is
// in front of the user, so no conversation is in the foreground even though the
// backend's active session has not changed.
func TestForegroundSessionOnOtherScreen(t *testing.T) {
	chat := &foregroundChat{active: "sess-1"}
	root, tester := foregroundRoot(t, chat)
	foreground := root.ForegroundSession()
	pushAndFrame(t, root, tester, "insights", "insights-body")
	if id, ok := foreground.ForegroundSession(); ok || id != "" {
		t.Fatalf("ForegroundSession() on the insights screen = %q, %t; want %q, false", id, ok, "")
	}
	// Returning to the conversation makes it the foreground one again.
	pushAndFrame(t, root, tester, sessionScreenID, "session-body")
	if id, ok := foreground.ForegroundSession(); !ok || id != "sess-1" {
		t.Fatalf("ForegroundSession() back on the conversation screen = %q, %t; want %q, true", id, ok, "sess-1")
	}
}

// TestForegroundSessionChatFromKernel covers the host wiring order: the desktop
// provides the service before the agent provides KeyChat, so the service must
// resolve the chat backend from the kernel on each call.
func TestForegroundSessionChatFromKernel(t *testing.T) {
	chat := &foregroundChat{active: "sess-kernel"}
	kernel := kernelWith(t, func(kernel cordis.Context) {
		if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(chat)); err != nil {
			t.Fatalf("provide chat: %v", err)
		}
	})
	root, tester := foregroundRoot(t, nil)
	root.SetKernelContext(kernel)
	foreground := root.ForegroundSession()
	pushAndFrame(t, root, tester, sessionScreenID, "session-body")
	if id, ok := foreground.ForegroundSession(); !ok || id != "sess-kernel" {
		t.Fatalf("ForegroundSession() from the kernel chat = %q, %t; want %q, true", id, ok, "sess-kernel")
	}
}

// TestForegroundSessionWithoutChat covers a stripped embedder: no chat backend
// means no conversation in the foreground.
func TestForegroundSessionWithoutChat(t *testing.T) {
	root, tester := foregroundRoot(t, nil)
	foreground := root.ForegroundSession()
	pushAndFrame(t, root, tester, sessionScreenID, "session-body")
	if id, ok := foreground.ForegroundSession(); ok || id != "" {
		t.Fatalf("ForegroundSession() without a chat backend = %q, %t; want %q, false", id, ok, "")
	}
}

// TestForegroundSessionEmptyActive covers the no-open-conversation case: the
// active session id is empty, so nothing is in the foreground.
func TestForegroundSessionEmptyActive(t *testing.T) {
	chat := &foregroundChat{active: "   "}
	root, tester := foregroundRoot(t, chat)
	foreground := root.ForegroundSession()
	pushAndFrame(t, root, tester, sessionScreenID, "session-body")
	if id, ok := foreground.ForegroundSession(); ok || id != "" {
		t.Fatalf("ForegroundSession() with an empty active session = %q, %t; want %q, false", id, ok, "")
	}
}

// TestForegroundSessionNilRoot covers the embedder's nil guard.
func TestForegroundSessionNilRoot(t *testing.T) {
	var root *Root
	if service := root.ForegroundSession(); service != nil {
		t.Fatalf("a nil root exposes a foreground-session service: %v", service)
	}
}
