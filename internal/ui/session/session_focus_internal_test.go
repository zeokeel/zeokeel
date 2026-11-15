package session

// Unit tests for the durable-locator resolution behind contracts.SessionFocus:
// the stored history keeps tool result messages that the transcript does not
// render as their own messages, so a search hit's stored position must be
// translated to the transcript message position, not used as a raw index.

import (
	"context"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// focusHistory is a rig.History returning scripted durable messages.
type focusHistory struct {
	messages []rig.Message
}

func (h focusHistory) Load(context.Context, string) ([]rig.Message, error) {
	return append([]rig.Message(nil), h.messages...), nil
}

func (h focusHistory) Append(context.Context, string, ...rig.Message) error { return nil }

// focusStore is a contracts.Store whose History view is scripted; the other
// methods are never called by the resolver.
type focusStore struct {
	contracts.Store
	history rig.History
}

func (s focusStore) History() rig.History { return s.history }

// TestTranscriptRowForMessageLocatorSkipsToolMessages covers the durable
// position mapping: a hit on the second user message (stored position 4, after
// the first turn's tool result at position 3) must scroll to the transcript's
// second user message (index 2), not to the transcript's fourth message.
func TestTranscriptRowForMessageLocatorSkipsToolMessages(t *testing.T) {
	ctx := cordis.Root()
	defer func() { _ = ctx.DisposeAll() }()
	history := focusHistory{messages: []rig.Message{
		rig.NewMessage(rig.RoleUser, "问题一"),
		{Role: rig.RoleAssistant, ToolCalls: []rig.ToolCall{{ID: "call-1", Name: "bash"}}},
		{Role: rig.RoleTool, ToolCallID: "call-1", Content: []rig.ContentPart{rig.TextPart("工具结果")}},
		rig.NewMessage(rig.RoleUser, "问题二"),
		rig.NewMessage(rig.RoleAssistant, "回答二"),
	}}
	if _, err := ctx.Provide(contracts.KeyStore, contracts.Store(focusStore{history: history})); err != nil {
		t.Fatalf("provide store: %v", err)
	}
	st := &state{ctx: ctx}
	chatSession := contracts.ChatSession{ID: "s1", Messages: []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题一"},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答一"},
		{ID: "u2", Role: contracts.ChatRoleUser, Text: "问题二"},
		{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "回答二"},
	}}
	messages := transcriptMessages(st, chatSession)
	mapping := uikit.BuildSessionRowMap(messages, st.transcriptSameTurn())
	want := mapping.RowOf(2) // 问题二
	if got := transcriptRowForMessageLocator(st, chatSession, contracts.MessageOrdinalLocator(4)); got != want {
		t.Fatalf("durable position 4 mapped to row %d, want the 问题二 row %d", got, want)
	}
	if got := transcriptRowForMessageLocator(st, chatSession, contracts.MessageOrdinalLocator(1)); got != mapping.RowOf(0) {
		t.Fatalf("durable position 1 mapped to row %d, want the 问题一 row %d", got, mapping.RowOf(0))
	}
	// A tool result position resolves to the assistant message that ran the call
	// (the transcript's nearest preceding user/assistant row).
	if got := transcriptRowForMessageLocator(st, chatSession, contracts.MessageOrdinalLocator(3)); got != mapping.RowOf(1) {
		t.Fatalf("durable position 3 mapped to row %d, want the 回答一 row %d", got, mapping.RowOf(1))
	}
	// Out-of-range positions stay unresolved.
	if got := transcriptRowForMessageLocator(st, chatSession, contracts.MessageOrdinalLocator(99)); got != -1 {
		t.Fatalf("out-of-range position mapped to row %d, want -1", got)
	}
}

// TestTranscriptRowForMessageLocatorWithoutStore covers the host without the
// durable store: the stored order is the transcript order (no tool messages to
// skip), so the position maps directly.
func TestTranscriptRowForMessageLocatorWithoutStore(t *testing.T) {
	st := &state{}
	chatSession := contracts.ChatSession{ID: "s1", Messages: []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题一"},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答一"},
		{ID: "u2", Role: contracts.ChatRoleUser, Text: "问题二"},
	}}
	if got := transcriptRowForMessageLocator(st, chatSession, contracts.MessageOrdinalLocator(3)); got != 2 {
		t.Fatalf("position 3 without a store mapped to row %d, want 2", got)
	}
	// A live message id still wins over the durable position.
	if got := transcriptRowForMessageLocator(st, chatSession, "a1"); got != 1 {
		t.Fatalf("live id mapped to row %d, want 1", got)
	}
}
