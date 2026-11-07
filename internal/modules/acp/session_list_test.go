package acp

// Tests for session/list and session/load.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// listChat is a ChatService with deterministic session metadata for list/load.
type listChat struct {
	*fakeChat
	infos   []contracts.ChatSessionInfo
	session map[string]contracts.ChatSession
	active  string
}

func newListChat(infos []contracts.ChatSessionInfo, sessions map[string]contracts.ChatSession) *listChat {
	return &listChat{fakeChat: newFakeChat(), infos: infos, session: sessions}
}

func (c *listChat) Sessions() []contracts.ChatSessionInfo {
	return append([]contracts.ChatSessionInfo(nil), c.infos...)
}

func (c *listChat) Session(id string) (contracts.ChatSession, bool) {
	session, ok := c.session[id]
	return session, ok
}

func (c *listChat) ActiveSession() string { return c.active }
func (c *listChat) SetActiveSession(id string) error {
	c.active = id
	return nil
}

func sessionListLine(id any) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"session/list","params":{}}`, jsonValue(id))
}

func sessionLoadLine(id any, sessionID string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"session/load","params":{"sessionId":%q}}`,
		jsonValue(id), sessionID)
}

func frameErrorCode(t *testing.T, frame wireFrame) int {
	t.Helper()
	if frame.Error == nil {
		t.Fatalf("frame has no error: %+v", frame)
	}
	return frame.Error.Code
}

func TestSessionListSortedByUpdatedAt(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := newListChat([]contracts.ChatSessionInfo{
		{ID: "old", Title: "Old", UpdatedAt: base.Add(-time.Hour)},
		{ID: "new", Title: "New", UpdatedAt: base},
		{ID: "mid", Title: "Mid", UpdatedAt: base.Add(-time.Minute)},
	}, nil)
	service := newService(Config{}, chat, nil)
	frames := serveLines(t, service, initializeLine(1), sessionListLine(2))
	if len(frames) != 2 || frames[0].Error != nil || frames[1].Error != nil {
		t.Fatalf("frames = %+v", frames)
	}
	var result struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
			Title     string `json:"title"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(frames[1].Result, &result); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	if len(result.Sessions) != 3 || result.Sessions[0].SessionID != "new" ||
		result.Sessions[1].SessionID != "mid" || result.Sessions[2].SessionID != "old" {
		t.Fatalf("sessions = %+v", result.Sessions)
	}
	if result.Sessions[0].Title != "New" || result.Sessions[0].UpdatedAt == "" {
		t.Fatalf("newest session shape = %+v", result.Sessions[0])
	}
}

func TestSessionListEmpty(t *testing.T) {
	service := newService(Config{}, newListChat(nil, nil), nil)
	frames := serveLines(t, service, sessionListLine(1))
	if len(frames) != 1 || frames[0].Error != nil {
		t.Fatalf("frames = %+v", frames)
	}
	var result struct {
		Sessions []any `json:"sessions"`
	}
	if err := json.Unmarshal(frames[0].Result, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Sessions == nil || len(result.Sessions) != 0 {
		t.Fatalf("empty sessions = %#v", result.Sessions)
	}
}

func TestSessionLoadKnownAndUnknown(t *testing.T) {
	session := contracts.ChatSession{
		ID: "sess-1", Title: "Loaded", Model: "deepseek-chat",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "hello"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "hi there"},
			{ID: "t1", Role: contracts.ChatRoleTool, Text: "ignored tool text"},
			{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "second answer"},
		},
	}
	chat := newListChat(
		[]contracts.ChatSessionInfo{{ID: "sess-1", Title: "Loaded", UpdatedAt: time.Unix(1700000000, 0)}},
		map[string]contracts.ChatSession{"sess-1": session},
	)
	chat.active = "sess-1"
	service := newService(Config{}, chat, nil)
	frames := serveLines(t, service, sessionLoadLine(1, "sess-1"))
	if len(frames) != 1 || frames[0].Error != nil {
		t.Fatalf("load frames = %+v", frames)
	}
	var result struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
		Model     string `json:"model"`
		Messages  []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(frames[0].Result, &result); err != nil {
		t.Fatalf("decode load: %v", err)
	}
	if result.SessionID != "sess-1" || result.Title != "Loaded" || result.Model != "deepseek-chat" {
		t.Fatalf("load result = %+v", result)
	}
	if len(result.Messages) != 3 || result.Messages[0].Role != "user" || result.Messages[0].Text != "hello" ||
		result.Messages[1].Role != "assistant" || result.Messages[1].Text != "hi there" ||
		result.Messages[2].Text != "second answer" {
		t.Fatalf("load messages = %+v", result.Messages)
	}
	if chat.active != "sess-1" {
		t.Fatalf("load changed active session to %q", chat.active)
	}

	// Unknown session is a readable invalid-params error.
	frames = serveLines(t, service, sessionLoadLine(2, "missing"))
	if len(frames) != 1 || frameErrorCode(t, frames[0]) != CodeInvalidParams {
		t.Fatalf("unknown load frames = %+v", frames)
	}
}

func TestSessionLoadAndListBadParams(t *testing.T) {
	service := newService(Config{}, newListChat(nil, nil), nil)
	badList := `{"jsonrpc":"2.0","id":1,"method":"session/list","params":"not-object"}`
	badLoad := `{"jsonrpc":"2.0","id":2,"method":"session/load","params":"not-object"}`
	missingID := `{"jsonrpc":"2.0","id":3,"method":"session/load","params":{}}`
	frames := serveLines(t, service, badList, badLoad, missingID)
	if len(frames) != 3 {
		t.Fatalf("frames = %+v", frames)
	}
	for _, frame := range frames {
		if frameErrorCode(t, frame) != CodeInvalidParams {
			t.Fatalf("bad params frame = %+v", frame)
		}
	}
}

func TestInitializeAdvertisesLoadSession(t *testing.T) {
	service := newService(Config{}, newListChat(nil, nil), nil)
	frames := serveLines(t, service, initializeLine(1))
	if len(frames) != 1 || frames[0].Error != nil {
		t.Fatalf("initialize frames = %+v", frames)
	}
	var result struct {
		AgentCapabilities map[string]any `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(frames[0].Result, &result); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if result.AgentCapabilities["loadSession"] != true {
		t.Fatalf("agentCapabilities = %+v, want loadSession true", result.AgentCapabilities)
	}
}
