package regression

// Adversarial audit: @session references, supplementary invariants beyond the
// round-2 injection/persistence checks: a durable session_refs event records only
// the actually-expanded references (self/unknown/duplicates filtered), a prompt
// without references records no event, and the injected transcript is bounded to
// the last 20 messages of the referenced session.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
)

// sessionRefEvents returns the decoded session_refs events of a session.
func sessionRefEvents(t *testing.T, store contracts.Store, sessionID string) [][]string {
	t.Helper()
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events(%q): %v", sessionID, err)
	}
	references := make([][]string, 0)
	for _, event := range events {
		if event.Type != "session_refs" {
			continue
		}
		var payload struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("session_refs payload is not JSON: %v", err)
		}
		references = append(references, payload.IDs)
	}
	return references
}

// TestSessionReferenceEventRecordsExpandedIDs covers the durable reference event:
// exactly one session_refs event with the resolved IDs (self, unknown and
// duplicate references filtered), and none for a prompt without references.
func TestSessionReferenceEventRecordsExpandedIDs(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		return 0, sseAnswer("ack " + decodeRequestContent(body, "user"))
	})
	store := requireService(t, harness.h.ctx, contracts.KeyStore)

	referenced := harness.newChatSession(t, "被引用")
	harness.sendAndWait(t, referenced, "seed")

	referencing := harness.newChatSession(t, "引用方")
	prompt := contracts.FormatSessionReference(referenced) + " " +
		contracts.FormatSessionReference("missing-session-id") + " " +
		contracts.FormatSessionReference(referencing) + " " +
		contracts.FormatSessionReference(referenced) + " summarize"
	harness.sendAndWait(t, referencing, prompt)

	references := sessionRefEvents(t, store, referencing)
	if len(references) != 1 {
		t.Fatalf("session_refs events = %v, want exactly one", references)
	}
	if len(references[0]) != 1 || references[0][0] != referenced {
		t.Fatalf("session_refs ids = %v, want only the resolved reference %q", references[0], referenced)
	}
	if !harness.h.logger.contains(`session reference "missing-session-id": unknown session`) {
		t.Fatalf("unknown reference was not logged: %v", harness.h.logger.lines)
	}

	// A prompt without references records no session_refs event.
	plain := harness.newChatSession(t, "普通会话")
	harness.sendAndWait(t, plain, "no mentions here")
	if got := sessionRefEvents(t, store, plain); len(got) != 0 {
		t.Fatalf("plain prompt recorded session_refs events: %v", got)
	}
}

// TestSessionReferenceTailTwentyMessages covers the transcript bound: only the
// last 20 messages of the referenced session are injected (here the block is far
// below the 8000-rune cap, so the tail bound, not truncation, is what excludes
// the earlier messages).
func TestSessionReferenceTailTwentyMessages(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		return 0, sseAnswer("ack " + decodeRequestContent(body, "user"))
	})

	referenced := harness.newChatSession(t, "长会话")
	for index := 0; index < 13; index++ {
		harness.sendAndWait(t, referenced, "seed-msg-"+twoDigits(index))
	}
	readModel, ok := harness.chat.Session(referenced)
	if !ok {
		t.Fatalf("Session(%q) missing", referenced)
	}
	if len(readModel.Messages) != 26 {
		t.Fatalf("referenced session has %d messages, want 26 (13 turns)", len(readModel.Messages))
	}

	referencing := harness.newChatSession(t, "引用长会话")
	marker := len(harness.model.callsSnapshot())
	prompt := contracts.FormatSessionReference(referenced) + " summarize the tail"
	harness.sendAndWait(t, referencing, prompt)
	body := referenceRunBody(t, harness, marker)
	content := requestContent(t, body, "user")
	block := strings.TrimSuffix(content, prompt)

	// Messages 0..5 (turns 0..2) fall outside the last-20 window.
	for _, excluded := range []string{"seed-msg-00", "seed-msg-01", "seed-msg-02"} {
		if strings.Contains(block, excluded) {
			t.Fatalf("injected block contains %q, want only the last 20 messages: %q", excluded, block)
		}
	}
	if !strings.Contains(block, "seed-msg-12") {
		t.Fatalf("injected block lacks the newest message: %q", block)
	}
	if runes := len([]rune(block)); runes > 8000 || strings.Contains(block, "…") {
		t.Fatalf("block = %d runes (truncated=%v), want a complete tail-20 injection under the cap",
			runes, strings.Contains(block, "…"))
	}
	// The durable read model still has the raw prompt, not the expansion.
	store := requireService(t, harness.h.ctx, contracts.KeyStore)
	history, err := store.History().Load(context.Background(), referencing)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	var lastUser string
	for _, message := range history {
		if message.Role == rig.RoleUser {
			lastUser = message.Text()
		}
	}
	if lastUser != prompt {
		t.Fatalf("durable prompt = %q, want the raw prompt %q", lastUser, prompt)
	}
}

// twoDigits formats a small index with a leading zero.
func twoDigits(index int) string {
	if index < 10 {
		return "0" + itoa(int64(index))
	}
	return itoa(int64(index))
}
