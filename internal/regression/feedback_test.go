package regression

// Adversarial audit: per-message feedback (task-133, landed in the working tree
// during this round). The rating is a per-message settings row, hydrated into the
// read model, restart-safe and only committed after a successful settings write.
// Negative cases: invalid values, unknown session/message targets, write
// failures, concurrent toggles and clearing.
//
// Note: the agent module requires contracts.KeySettings, so the nil-settings
// branch (ErrSettingsUnavailable) is not reachable through the public assembly;
// it is covered by the module's own package tests.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
)

// faultSettings delegates to an in-memory store but can reject writes for the
// feedback settings prefix (write-failure injection for SetMessageFeedback).
type faultSettings struct {
	inner contracts.SettingsService
	mu    sync.Mutex
	fail  bool
}

func (s *faultSettings) Get(key, fallback string) string { return s.inner.Get(key, fallback) }

func (s *faultSettings) Set(key, value string) error {
	s.mu.Lock()
	reject := s.fail && strings.HasPrefix(key, "feedback.")
	s.mu.Unlock()
	if reject {
		return errors.New("settings write denied by the audit fault")
	}
	return s.inner.Set(key, value)
}

func (s *faultSettings) All() map[string]string { return s.inner.All() }

// requireFeedbacker asserts and returns the chat service's feedback half.
func requireFeedbacker(t *testing.T, chat contracts.ChatService) contracts.SessionFeedbacker {
	t.Helper()
	feedbacker, ok := chat.(contracts.SessionFeedbacker)
	if !ok {
		t.Fatal("the chat service does not implement contracts.SessionFeedbacker")
	}
	return feedbacker
}

// newFeedbackSession sends one prompt and returns the session plus its last
// assistant message (the rating target).
func newFeedbackSession(t *testing.T, harness *agentHarness) (string, contracts.ChatMessage) {
	t.Helper()
	sessionID := harness.newChatSession(t, "feedback")
	session := harness.sendAndWait(t, sessionID, "rate this answer")
	assistant := lastAssistant(t, session)
	if assistant.ID == "" {
		t.Fatalf("assistant message has no ID: %+v", assistant)
	}
	return sessionID, assistant
}

// TestFeedbackInvalidValuesAndTargets covers rejected input: malformed ratings
// and unknown/empty session or message IDs return errors without writing any
// setting or changing the read model.
func TestFeedbackInvalidValuesAndTargets(t *testing.T) {
	harness := startAgent(t, agent.Config{}, nil)
	feedbacker := requireFeedbacker(t, harness.chat)
	sessionID, assistant := newFeedbackSession(t, harness)

	for _, invalid := range []string{"great", "GOOD", " good ", "bad\n", "1", "true"} {
		if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, invalid); err == nil {
			t.Fatalf("SetMessageFeedback(%q) succeeded, want a validation error", invalid)
		} else if !strings.Contains(err.Error(), "invalid message feedback") {
			t.Fatalf("SetMessageFeedback(%q) error = %v, want the validation error", invalid, err)
		}
		if got := harness.h.settings.Get(contracts.MessageFeedbackSettingKey(sessionID, assistant.ID), ""); got != "" {
			t.Fatalf("invalid feedback %q persisted setting %q", invalid, got)
		}
	}
	session, _ := harness.chat.Session(sessionID)
	rated := false
	for _, message := range session.Messages {
		if message.ID == assistant.ID {
			rated = message.Feedback != ""
		}
	}
	if rated {
		t.Fatal("invalid feedback values changed the read model")
	}

	for name, call := range map[string]func() error{
		"unknown session": func() error {
			return feedbacker.SetMessageFeedback("missing-session", assistant.ID, contracts.FeedbackGood)
		},
		"unknown message": func() error {
			return feedbacker.SetMessageFeedback(sessionID, "missing-message", contracts.FeedbackGood)
		},
		"empty session":      func() error { return feedbacker.SetMessageFeedback("", assistant.ID, contracts.FeedbackGood) },
		"empty message":      func() error { return feedbacker.SetMessageFeedback(sessionID, "", contracts.FeedbackGood) },
		"whitespace session": func() error { return feedbacker.SetMessageFeedback("   ", assistant.ID, contracts.FeedbackGood) },
	} {
		if err := call(); err == nil {
			t.Fatalf("%s: SetMessageFeedback succeeded, want an error", name)
		}
	}
	// A valid rating still works after the failed calls.
	if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, contracts.FeedbackGood); err != nil {
		t.Fatalf("SetMessageFeedback after rejections: %v", err)
	}
}

// TestFeedbackToggleReadModelAndClear covers the rating lifecycle: good/bad are
// reflected in the read model and settings row immediately; "" clears both.
func TestFeedbackToggleReadModelAndClear(t *testing.T) {
	harness := startAgent(t, agent.Config{}, nil)
	feedbacker := requireFeedbacker(t, harness.chat)
	sessionID, assistant := newFeedbackSession(t, harness)
	key := contracts.MessageFeedbackSettingKey(sessionID, assistant.ID)

	for _, value := range []string{contracts.FeedbackGood, contracts.FeedbackBad} {
		if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, value); err != nil {
			t.Fatalf("SetMessageFeedback(%q): %v", value, err)
		}
		session, _ := harness.chat.Session(sessionID)
		got := ""
		for _, message := range session.Messages {
			if message.ID == assistant.ID {
				got = message.Feedback
			}
		}
		if got != value {
			t.Fatalf("read-model Feedback = %q after setting %q", got, value)
		}
		if stored := harness.h.settings.Get(key, ""); stored != value {
			t.Fatalf("settings row = %q after setting %q", stored, value)
		}
	}

	// Clearing writes an empty row and clears the read model.
	if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, ""); err != nil {
		t.Fatalf("SetMessageFeedback(clear): %v", err)
	}
	session, _ := harness.chat.Session(sessionID)
	for _, message := range session.Messages {
		if message.ID == assistant.ID && message.Feedback != "" {
			t.Fatalf("read-model Feedback = %q after clearing", message.Feedback)
		}
	}
	if stored := harness.h.settings.Get(key, ""); stored != "" {
		t.Fatalf("settings row = %q after clearing", stored)
	}
}

// TestFeedbackWriteFailureLeavesReadModelUnchanged covers a settings write
// failure: the error surfaces, no rating reaches the read model and no change
// notification is sent; a later successful write works.
func TestFeedbackWriteFailureLeavesReadModelUnchanged(t *testing.T) {
	inner := newMemSettings(nil)
	failing := &faultSettings{inner: inner}
	harness := newHarnessCoreWith(t, t.TempDir(), true, inner, failing)
	model := newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("answer") })
	agentHarness := startAgentOn(t, harness, agent.Config{}, model, approvals.ModeAuto)
	feedbacker := requireFeedbacker(t, agentHarness.chat)
	sessionID, assistant := newFeedbackSession(t, agentHarness)

	var notifications counter
	if _, err := agentHarness.chat.Subscribe(func(string) { notifications.inc() }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	failing.mu.Lock()
	failing.fail = true
	failing.mu.Unlock()

	if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, contracts.FeedbackGood); err == nil {
		t.Fatal("SetMessageFeedback succeeded while the settings write was denied")
	} else if !strings.Contains(err.Error(), "persist feedback") {
		t.Fatalf("write-failure error = %v, want the persist error", err)
	}
	session, _ := agentHarness.chat.Session(sessionID)
	for _, message := range session.Messages {
		if message.ID == assistant.ID && message.Feedback != "" {
			t.Fatalf("read-model Feedback = %q after a failed write", message.Feedback)
		}
	}
	if got := notifications.load(); got != 0 {
		t.Fatalf("failed feedback notified subscribers %d times, want 0", got)
	}

	failing.mu.Lock()
	failing.fail = false
	failing.mu.Unlock()
	if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, contracts.FeedbackBad); err != nil {
		t.Fatalf("SetMessageFeedback after clearing the fault: %v", err)
	}
}

// TestFeedbackRestoredFromSettings covers restart persistence: the rating is a
// settings row, so a restarted agent hydrates it into the restored read model.
func TestFeedbackRestoredFromSettings(t *testing.T) {
	first := newHarness(t)
	firstModel := newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("persisted answer") })
	firstAgent := startAgentOn(t, first, agent.Config{}, firstModel, approvals.ModeAuto)
	feedbacker := requireFeedbacker(t, firstAgent.chat)
	sessionID, assistant := newFeedbackSession(t, firstAgent)
	if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, contracts.FeedbackGood); err != nil {
		t.Fatalf("SetMessageFeedback: %v", err)
	}
	if err := first.ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first root: %v", err)
	}

	// The process host persists settings across restarts; reuse the same settings
	// store (the real host reloads its settings file from the same data dir).
	second := newHarnessCoreWith(t, first.dataDir, true, first.settings, contracts.SettingsService(first.settings))
	secondModel := newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("unused") })
	restoredAgent := startAgentOn(t, second, agent.Config{}, secondModel, approvals.ModeAuto)
	session, ok := restoredAgent.chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) was not restored", sessionID)
	}
	found := false
	for _, message := range session.Messages {
		if message.ID != assistant.ID {
			continue
		}
		found = true
		if message.Feedback != contracts.FeedbackGood {
			t.Fatalf("restored Feedback = %q, want %q (from the settings row)", message.Feedback, contracts.FeedbackGood)
		}
	}
	if !found {
		t.Fatalf("assistant message %q missing from the restored session", assistant.ID)
	}
}

// TestFeedbackConcurrentToggles covers concurrent rating writes: every call
// succeeds, the read model and settings row agree on the surviving value and
// clearing still works (-race).
func TestFeedbackConcurrentToggles(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func([]byte) (int, string) { return 0, sseAnswer("answer") })
	feedbacker := requireFeedbacker(t, harness.chat)
	sessionID, assistant := newFeedbackSession(t, harness)
	key := contracts.MessageFeedbackSettingKey(sessionID, assistant.ID)

	var wg sync.WaitGroup
	for index := 0; index < 20; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			value := contracts.FeedbackGood
			if index%2 == 0 {
				value = contracts.FeedbackBad
			}
			if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, value); err != nil {
				t.Errorf("concurrent SetMessageFeedback: %v", err)
			}
		}()
	}
	wg.Wait()

	session, _ := harness.chat.Session(sessionID)
	value := ""
	for _, message := range session.Messages {
		if message.ID == assistant.ID {
			value = message.Feedback
		}
	}
	if value != contracts.FeedbackGood && value != contracts.FeedbackBad {
		t.Fatalf("concurrent result Feedback = %q, want good or bad", value)
	}
	if stored := harness.h.settings.Get(key, ""); stored != value {
		t.Fatalf("settings row %q disagrees with read model %q", stored, value)
	}
	if err := feedbacker.SetMessageFeedback(sessionID, assistant.ID, ""); err != nil {
		t.Fatalf("clear after concurrency: %v", err)
	}
}

// TestFeedbackContractSurface pins the public contract a UI needs: the setting
// key shape and the accepted values.
func TestFeedbackContractSurface(t *testing.T) {
	key := contracts.MessageFeedbackSettingKey("sess", "msg")
	if key != "feedback.sess.msg" {
		t.Fatalf("MessageFeedbackSettingKey = %q, want feedback.sess.msg", key)
	}
	if contracts.FeedbackGood != "good" || contracts.FeedbackBad != "bad" {
		t.Fatalf("feedback constants = %q/%q, want good/bad", contracts.FeedbackGood, contracts.FeedbackBad)
	}
}

// TestFeedbackOnNonAssistantMessagesProbe pins the AUDIT-P3-12 fix: only
// assistant turns can be rated, so a user/tool message id is rejected with a
// readable role error (previously it was accepted silently).
func TestFeedbackOnNonAssistantMessagesProbe(t *testing.T) {
	harness := startAgent(t, agent.Config{}, nil)
	feedbacker := requireFeedbacker(t, harness.chat)
	sessionID := harness.newChatSession(t, "feedback-roles")
	session := harness.sendAndWait(t, sessionID, "rate roles")
	var userMessage, assistantID string
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleUser && userMessage == "" {
			userMessage = message.ID
		}
		if message.Role == contracts.ChatRoleAssistant {
			assistantID = message.ID
		}
	}
	if userMessage == "" || assistantID == "" {
		t.Fatalf("session lacks a user/assistant message: %+v", session.Messages)
	}
	err := feedbacker.SetMessageFeedback(sessionID, userMessage, contracts.FeedbackGood)
	if err == nil {
		t.Fatalf("feedback accepted on a user message (%q); want a role error", userMessage)
	}
	if !strings.Contains(err.Error(), "assistant") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("feedback on a user message error = %v, want a role/unknown error", err)
	}
}
