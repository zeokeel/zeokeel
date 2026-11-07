package session_test

// Tests for assistant-message good/bad feedback buttons.

import (
	"errors"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

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

func feedbackSessionChat() *feedbackChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "Feedback chat",
		Messages: []contracts.ChatMessage{
			{ID: "msg-user", Role: contracts.ChatRoleUser, Text: "rate this"},
			{ID: "msg-assistant", Role: contracts.ChatRoleAssistant, Text: "an answer"},
		},
	}, true)
	return &feedbackChat{fakeChat: chat}
}

func TestFeedbackButtonsToggle(t *testing.T) {
	chat := feedbackSessionChat()
	h := newFeedbackHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "有帮助", "没帮助")

	// good: write good; clicking it again clears.
	h.click(tester, "有帮助")
	call := chat.lastCall(t)
	if call.sessionID != "sess-1" || call.messageID != "msg-assistant" || call.feedback != contracts.FeedbackGood {
		t.Fatalf("good feedback call = %+v", call)
	}
	h.assertTexts(tester, "已反馈")
	h.click(tester, "有帮助")
	if call := chat.lastCall(t); call.feedback != "" {
		t.Fatalf("toggle clear call = %+v", call)
	}

	// bad: write bad.
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
}

func TestFeedbackButtonsShowError(t *testing.T) {
	chat := feedbackSessionChat()
	chat.setErr = errors.New("feedback write failed")
	h := newFeedbackHarness(t, chat)
	tester := h.tester()
	h.click(tester, "有帮助")
	h.assertTexts(tester, "feedback write failed")
}

func TestFeedbackButtonsEnglish(t *testing.T) {
	chat := feedbackSessionChat()
	h := newFeedbackHarness(t, chat)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	h.assertTexts(tester, "Helpful", "Not helpful")
	h.click(tester, "Helpful")
	h.assertTexts(tester, "Rated")
}
