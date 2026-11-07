package agent

// Tests for assistant-message feedback persistence and restore.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func newFeedbackService(t *testing.T, store *fakeStore, settings contracts.SettingsService,
	approvals contracts.Approvals) *service {
	t.Helper()
	if store == nil {
		store = newFakeStore()
	}
	return newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, store, approvals)
}

// sendFeedbackMessage creates a session with one completed user+assistant turn.
func sendFeedbackMessage(t *testing.T, svc *service) (string, contracts.ChatMessage) {
	t.Helper()
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("feedback")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "rate me"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == contracts.ChatRoleAssistant {
			return id, session.Messages[index]
		}
	}
	t.Fatalf("no assistant message: %+v", session.Messages)
	return "", contracts.ChatMessage{}
}

func TestSetMessageFeedbackReadModel(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newFeedbackService(t, nil, contracts.SettingsService(settings), nil)
	id, assistant := sendFeedbackMessage(t, svc)
	if assistant.ID == "" {
		t.Fatal("assistant message has no id")
	}
	if err := svc.SetMessageFeedback(id, assistant.ID, contracts.FeedbackGood); err != nil {
		t.Fatalf("SetMessageFeedback(good): %v", err)
	}
	session, _ := svc.Session(id)
	if got := session.Messages[len(session.Messages)-1].Feedback; got != contracts.FeedbackGood {
		t.Fatalf("feedback after set = %q, want good", got)
	}
	if got := settings.Get(contracts.MessageFeedbackSettingKey(id, assistant.ID), ""); got != contracts.FeedbackGood {
		t.Fatalf("stored feedback = %q, want good", got)
	}
	// Switching to bad replaces the value.
	if err := svc.SetMessageFeedback(id, assistant.ID, contracts.FeedbackBad); err != nil {
		t.Fatalf("SetMessageFeedback(bad): %v", err)
	}
	session, _ = svc.Session(id)
	if got := session.Messages[len(session.Messages)-1].Feedback; got != contracts.FeedbackBad {
		t.Fatalf("feedback after switch = %q, want bad", got)
	}
	// Empty clears the stored value and the read model.
	if err := svc.SetMessageFeedback(id, assistant.ID, ""); err != nil {
		t.Fatalf("SetMessageFeedback(clear): %v", err)
	}
	session, _ = svc.Session(id)
	if got := session.Messages[len(session.Messages)-1].Feedback; got != "" {
		t.Fatalf("feedback after clear = %q, want empty", got)
	}
	if got := settings.Get(contracts.MessageFeedbackSettingKey(id, assistant.ID), "missing"); got != "" {
		t.Fatalf("stored feedback after clear = %q, want empty", got)
	}
}

func TestSetMessageFeedbackValidation(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newFeedbackService(t, nil, contracts.SettingsService(settings), nil)
	id, assistant := sendFeedbackMessage(t, svc)
	if err := svc.SetMessageFeedback("missing-session", assistant.ID, contracts.FeedbackGood); err == nil ||
		!strings.Contains(err.Error(), "unknown chat session") {
		t.Fatalf("unknown session = %v", err)
	}
	if err := svc.SetMessageFeedback(id, "missing-message", contracts.FeedbackGood); err == nil ||
		!strings.Contains(err.Error(), "unknown message") {
		t.Fatalf("unknown message = %v", err)
	}
	if err := svc.SetMessageFeedback(id, assistant.ID, "great"); err == nil {
		t.Fatal("invalid feedback value was accepted")
	}
	if got := settings.Get(contracts.MessageFeedbackSettingKey(id, assistant.ID), ""); got != "" {
		t.Fatalf("invalid feedback wrote %q", got)
	}
}

func TestSetMessageFeedbackRequiresSettings(t *testing.T) {
	svc := newFeedbackService(t, nil, nil, nil)
	id, err := svc.NewSession("no settings")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	svc.sessions[id].messages = []contracts.ChatMessage{{ID: "msg-x", Role: contracts.ChatRoleAssistant, Text: "hi"}}
	svc.mu.Unlock()
	if err := svc.SetMessageFeedback(id, "msg-x", contracts.FeedbackGood); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("SetMessageFeedback without settings = %v, want ErrSettingsUnavailable", err)
	}
}

// failingFeedbackSettings wraps fakeSettings with a write error.
type failingFeedbackSettings struct {
	*fakeSettings
	err error
}

func (s *failingFeedbackSettings) Set(string, string) error { return s.err }

func TestSetMessageFeedbackWriteFailure(t *testing.T) {
	base := newFakeSettings(nil)
	settings := &failingFeedbackSettings{fakeSettings: base, err: errors.New("disk full")}
	svc := newFeedbackService(t, nil, contracts.SettingsService(settings), nil)
	id, assistant := sendFeedbackMessage(t, svc)
	err := svc.SetMessageFeedback(id, assistant.ID, contracts.FeedbackGood)
	if err == nil || !strings.Contains(err.Error(), "disk full") || !strings.Contains(err.Error(), "feedback") {
		t.Fatalf("SetMessageFeedback write failure = %v", err)
	}
	session, _ := svc.Session(id)
	if got := session.Messages[len(session.Messages)-1].Feedback; got != "" {
		t.Fatalf("feedback after failed write = %q, want empty", got)
	}
}

func TestMessageFeedbackSurvivesRestore(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(nil)
	first := newFeedbackService(t, store, contracts.SettingsService(settings), nil)
	id, assistant := sendFeedbackMessage(t, first)
	if err := first.SetMessageFeedback(id, assistant.ID, contracts.FeedbackBad); err != nil {
		t.Fatalf("SetMessageFeedback: %v", err)
	}

	second := newFeedbackService(t, store, contracts.SettingsService(settings), nil)
	second.restore(context.Background())
	session, ok := second.Session(id)
	if !ok {
		t.Fatalf("restored session %q missing", id)
	}
	var restored *contracts.ChatMessage
	for index := range session.Messages {
		if session.Messages[index].Role == contracts.ChatRoleAssistant {
			restored = &session.Messages[index]
		}
	}
	if restored == nil || restored.Feedback != contracts.FeedbackBad {
		t.Fatalf("restored assistant = %+v, want feedback bad", restored)
	}
}

func TestSetMessageFeedbackConcurrent(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newFeedbackService(t, nil, contracts.SettingsService(settings), nil)
	id, assistant := sendFeedbackMessage(t, svc)
	var wg sync.WaitGroup
	for index := 0; index < 12; index++ {
		value := contracts.FeedbackGood
		if index%2 == 1 {
			value = contracts.FeedbackBad
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.SetMessageFeedback(id, assistant.ID, value); err != nil {
				t.Errorf("SetMessageFeedback: %v", err)
			}
			if _, ok := svc.Session(id); !ok {
				t.Error("Session disappeared")
			}
		}()
	}
	wg.Wait()
	session, _ := svc.Session(id)
	got := session.Messages[len(session.Messages)-1].Feedback
	if got != contracts.FeedbackGood && got != contracts.FeedbackBad {
		t.Fatalf("concurrent final feedback = %q", got)
	}
}

// TestSetMessageFeedbackRejectsNonAssistantMessages covers the role guard: the
// contract only rates assistant turns, so user/tool ids are rejected without
// touching settings.
func TestSetMessageFeedbackRejectsNonAssistantMessages(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newFeedbackService(t, nil, settings, nil)
	sessionID, _ := sendFeedbackMessage(t, svc)
	session, ok := svc.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionID)
	}
	var userID string
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleUser {
			userID = message.ID
			break
		}
	}
	if userID == "" {
		t.Fatalf("no user message in %+v", session.Messages)
	}
	if err := svc.SetMessageFeedback(sessionID, userID, contracts.FeedbackGood); err == nil {
		t.Fatal("SetMessageFeedback accepted a user message")
	}
	if got := settings.Get(contracts.MessageFeedbackSettingKey(sessionID, userID), ""); got != "" {
		t.Fatalf("rejected feedback wrote settings: %q", got)
	}
}
