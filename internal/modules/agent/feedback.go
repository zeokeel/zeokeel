package agent

// Message feedback: a user's good/bad rating of one assistant turn is stored as
// a per-message settings row (restart-safe) and hydrated into the read model
// when a session snapshot is built.

import (
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// messageFeedbackValue normalizes a stored feedback setting; malformed values
// are treated as unrated.
func messageFeedbackValue(settings contracts.SettingsService, sessionID, messageID string) string {
	if settings == nil || strings.TrimSpace(messageID) == "" {
		return ""
	}
	switch value := strings.TrimSpace(settings.Get(contracts.MessageFeedbackSettingKey(sessionID, messageID), "")); value {
	case contracts.FeedbackGood, contracts.FeedbackBad:
		return value
	default:
		return ""
	}
}

// SetMessageFeedback implements contracts.SessionFeedbacker. The read model is
// updated in place and subscribers are notified only after a successful write;
// an empty value clears the rating.
func (s *service) SetMessageFeedback(sessionID, messageID, feedback string) error {
	sessionID = strings.TrimSpace(sessionID)
	messageID = strings.TrimSpace(messageID)
	switch feedback {
	case "", contracts.FeedbackGood, contracts.FeedbackBad:
	default:
		return fmt.Errorf("agent: invalid message feedback %q (want \"\", %q or %q)",
			feedback, contracts.FeedbackGood, contracts.FeedbackBad)
	}
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	found := false
	assistant := false
	for _, message := range state.messages {
		if message.ID == messageID {
			found = true
			assistant = message.Role == contracts.ChatRoleAssistant
			break
		}
	}
	settings := s.settings
	s.mu.Unlock()
	if !found {
		return fmt.Errorf("agent: unknown message %q in session %q", messageID, sessionID)
	}
	// Ratings only apply to assistant turns (the contract and the UI agree);
	// rejecting other roles keeps the settings rows meaningful.
	if !assistant {
		return fmt.Errorf("agent: message %q in session %q is not an assistant message", messageID, sessionID)
	}
	if settings == nil {
		return ErrSettingsUnavailable
	}
	if err := settings.Set(contracts.MessageFeedbackSettingKey(sessionID, messageID), feedback); err != nil {
		return fmt.Errorf("agent: persist feedback of message %q: %w", messageID, err)
	}
	s.mu.Lock()
	if state, ok := s.sessions[sessionID]; ok {
		for index := range state.messages {
			if state.messages[index].ID == messageID {
				state.messages[index].Feedback = feedback
				break
			}
		}
	}
	s.mu.Unlock()
	s.notify(sessionID)
	return nil
}
