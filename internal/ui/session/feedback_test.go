package session_test

// Tests for assistant-message good/bad feedback, now the two radio items of the
// compact meta row's ⋯ message-actions menu.

import (
	"errors"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type feedbackCall struct {
	sessionID string
	messageID string
	feedback  string
}

// feedbackChat wraps the scripted ChatService with SessionFeedbacker and keeps
// the wrapped session read model in sync.
type feedbackChat struct {
	*fakeChat
	mu     sync.Mutex
	calls  []feedbackCall
	setErr error
}

func (f *feedbackChat) SetMessageFeedback(sessionID, messageID, feedback string) error {
	f.mu.Lock()
	f.calls = append(f.calls, feedbackCall{sessionID: sessionID, messageID: messageID, feedback: feedback})
	err := f.setErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	f.fakeChat.mu.Lock()
	if session, ok := f.fakeChat.sessions[sessionID]; ok {
		for index := range session.Messages {
			if session.Messages[index].ID == messageID {
				session.Messages[index].Feedback = feedback
				break
			}
		}
		f.fakeChat.sessions[sessionID] = session
	}
	f.fakeChat.mu.Unlock()
	f.fakeChat.emit(sessionID)
	return nil
}

func (f *feedbackChat) lastCall(t *testing.T) feedbackCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no feedback calls")
	}
	return f.calls[len(f.calls)-1]
}

func (f *feedbackChat) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newFeedbackHarness activates the session module with the wrapped chat service
// as contracts.KeyChat.
func newFeedbackHarness(t *testing.T, chat *feedbackChat) *harness {
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
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat.fakeChat}
}

// feedbackSessionChat is one answered assistant turn; the meta row's ⋯ menu is
// the only ratings entry.
func feedbackSessionChat() *feedbackChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "Feedback chat",
		Messages: []contracts.ChatMessage{
			{ID: "msg-assistant", Role: contracts.ChatRoleAssistant, Text: "an answer"},
		},
	}, true)
	return &feedbackChat{fakeChat: chat}
}

// openAssistantActions opens the ⋯ message-actions menu of the conversation's
// (only) assistant meta row.
func openAssistantActions(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "消息操作")
}

func TestFeedbackButtonsToggle(t *testing.T) {
	chat := feedbackSessionChat()
	h := newFeedbackHarness(t, chat)
	tester := h.tester()
	// The ratings live behind the ⋯ message-actions entry, not as resident
	// buttons.
	h.assertNoTexts(tester, "有帮助", "没帮助")
	openAssistantActions(t, h, tester)
	h.assertTexts(tester, "有帮助", "没帮助")

	// good: write good; choosing it again clears.
	h.click(tester, "有帮助")
	call := chat.lastCall(t)
	if call.sessionID != "sess-1" || call.messageID != "msg-assistant" || call.feedback != contracts.FeedbackGood {
		t.Fatalf("good feedback call = %+v", call)
	}
	h.assertNoTexts(tester, "已反馈")
	openAssistantActions(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "有帮助"); !ok || !info.Checked {
		t.Fatalf("helpful radio = %+v found %v, want checked", info, ok)
	}
	h.click(tester, "有帮助")
	if call := chat.lastCall(t); call.feedback != "" {
		t.Fatalf("toggle clear call = %+v", call)
	}

	// bad: write bad.
	openAssistantActions(t, h, tester)
	h.click(tester, "没帮助")
	if call := chat.lastCall(t); call.feedback != contracts.FeedbackBad {
		t.Fatalf("bad feedback call = %+v", call)
	}
	if calls := chat.callCount(); calls != 3 {
		t.Fatalf("feedback calls = %d, want 3", calls)
	}
}

func TestFeedbackButtonsHiddenWithoutImplementation(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "No feedback",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "answer"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertNoTexts(tester, "有帮助", "没帮助", "已反馈")
	openAssistantActions(t, h, tester)
	h.assertNoTexts(tester, "有帮助", "没帮助")
	h.assertTexts(tester, "复制消息")
}

func TestFeedbackButtonsShowError(t *testing.T) {
	chat := feedbackSessionChat()
	chat.setErr = errors.New("feedback write failed")
	h := newFeedbackHarness(t, chat)
	tester := h.tester()
	openAssistantActions(t, h, tester)
	h.click(tester, "有帮助")
	h.assertTexts(tester, "feedback write failed")
}

func TestFeedbackButtonsEnglish(t *testing.T) {
	chat := feedbackSessionChat()
	h := newFeedbackHarness(t, chat)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	h.click(tester, "Message actions")
	h.assertTexts(tester, "Helpful", "Not helpful")
	h.click(tester, "Helpful")
	h.assertNoTexts(tester, "Rated")
}

// TestFeedbackButtonsAreMenuRadios pins the official menu semantics: the two
// ratings are radio items (single choice, checked state reaches assistive
// technology), and choosing the checked one clears it.
func TestFeedbackButtonsAreMenuRadios(t *testing.T) {
	chat := feedbackSessionChat()
	h := newFeedbackHarness(t, chat)
	tester := h.tester()
	openAssistantActions(t, h, tester)

	good, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "有帮助")
	if !ok || good.Checked {
		t.Fatalf("helpful radio: %+v found %v, want an unchecked radio", good, ok)
	}
	bad, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "没帮助")
	if !ok || bad.Checked {
		t.Fatalf("not-helpful radio: %+v found %v, want an unchecked radio", bad, ok)
	}

	// Choosing helpful persists it; reopening marks that radio checked.
	h.click(tester, "有帮助")
	if call := chat.lastCall(t); call.feedback != contracts.FeedbackGood {
		t.Fatalf("helpful feedback call = %+v", call)
	}
	openAssistantActions(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "有帮助"); !ok || !info.Checked {
		t.Fatalf("helpful radio not checked after saving: %+v found %v", info, ok)
	}
	if info, _ := tester.AccessByRole(ui.RoleMenuItemRadio, "没帮助"); info.Checked {
		t.Fatalf("not-helpful radio checked while helpful is the rating: %+v", info)
	}

	// Choosing helpful again clears it.
	h.click(tester, "有帮助")
	if call := chat.lastCall(t); call.feedback != "" {
		t.Fatalf("toggle clear call = %+v", call)
	}
}
