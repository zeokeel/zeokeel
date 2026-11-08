package session

// Tests for the failed-turn error presentation: the session-level raw error row
// must not duplicate the localized per-message error row of the same failure.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSessionErrorVisibleDedupe(t *testing.T) {
	cases := []struct {
		name     string
		messages []contracts.ChatMessage
		session  string
		want     bool
	}{
		{
			name: "duplicate message error hides session row",
			messages: []contracts.ChatMessage{
				{Role: contracts.ChatRoleUser, Text: "hi"},
				{Role: contracts.ChatRoleAssistant, Error: "401 Unauthorized", ErrorKind: contracts.MessageErrorKindAuth},
			},
			session: "401 Unauthorized",
			want:    false,
		},
		{
			name: "cancelled message error hides session row",
			messages: []contracts.ChatMessage{
				{Role: contracts.ChatRoleUser, Text: "stop"},
				{Role: contracts.ChatRoleAssistant, Error: "cancelled", ErrorKind: contracts.MessageErrorKindCancelled},
			},
			session: "cancelled",
			want:    false,
		},
		{
			name: "unattached session error still shows",
			messages: []contracts.ChatMessage{
				{Role: contracts.ChatRoleUser, Text: "hi"},
			},
			session: "send failed: network down",
			want:    true,
		},
		{
			name:     "empty session error never shows",
			messages: []contracts.ChatMessage{{Role: contracts.ChatRoleUser, Text: "hi"}},
			session:  "",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionErrorVisible(tc.messages, tc.session); got != tc.want {
				t.Fatalf("sessionErrorVisible = %v, want %v", got, tc.want)
			}
		})
	}
}
