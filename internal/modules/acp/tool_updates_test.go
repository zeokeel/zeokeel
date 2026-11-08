package acp

// Tests for streaming agent tool calls as ACP session/update notifications:
// tool_call on start, tool_call_update on completion/failure with truncated
// content, generated call IDs for empty ones, empty-result content omission and
// the interleaved order with assistant text chunks.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// setAssistantTools replaces the last assistant message (creating one) with the
// text and tool-call read model, then notifies subscribers like the agent does.
func (c *fakeChat) setAssistantTools(sessionID, text string, tools []contracts.ToolCall, running bool) {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if !ok {
		session = &fakeSession{}
		c.sessions[sessionID] = session
	}
	if len(session.messages) == 0 || session.messages[len(session.messages)-1].Role != contracts.ChatRoleAssistant {
		session.messages = append(session.messages, contracts.ChatMessage{Role: contracts.ChatRoleAssistant})
	}
	message := &session.messages[len(session.messages)-1]
	message.Text = text
	message.Tools = append([]contracts.ToolCall(nil), tools...)
	session.running = running
	c.mu.Unlock()
	c.notifyAll(sessionID)
}

// notificationUpdates returns the session/update notifications in frame order.
func notificationUpdates(t *testing.T, frames []wireFrame) []map[string]any {
	t.Helper()
	var updates []map[string]any
	for _, frame := range frames {
		if frame.Method != MethodSessionUpdate {
			continue
		}
		var params struct {
			SessionID string         `json:"sessionId"`
			Update    map[string]any `json:"update"`
		}
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			t.Fatalf("decode notification %s: %v", frame.Params, err)
		}
		if params.Update == nil {
			t.Fatalf("notification missing update: %s", frame.Params)
		}
		updates = append(updates, params.Update)
	}
	return updates
}

func updateKind(update map[string]any) string {
	kind, _ := update["sessionUpdate"].(string)
	return kind
}

func TestToolCallStreamingOrder(t *testing.T) {
	chat := newFakeChat()
	call := contracts.ToolCall{ID: "call-1", Name: "read_file", Status: "running"}
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "Let me check.", nil, true)
		c.setAssistantTools(sessionID, "Let me check.", []contracts.ToolCall{call}, true)
		call.Status, call.Summary = "ok", "file contents"
		c.setAssistantTools(sessionID, "Let me check.", []contracts.ToolCall{call}, true)
		c.setAssistantTools(sessionID, "Let me check. Done reading.", []contracts.ToolCall{call}, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service, promptLine(1, "sess-1", "read it"))

	updates := notificationUpdates(t, frames)
	if len(updates) != 4 {
		t.Fatalf("updates = %+v, want text/tool_call/tool_call_update/text", updates)
	}
	if kind := updateKind(updates[0]); kind != UpdateAgentMessageChunk {
		t.Fatalf("update[0] kind = %q", kind)
	}
	if text := updates[0]["content"].(map[string]any)["text"]; text != "Let me check." {
		t.Fatalf("first text chunk = %v", text)
	}

	if kind := updateKind(updates[1]); kind != "tool_call" {
		t.Fatalf("update[1] kind = %q, want tool_call; %+v", kind, updates[1])
	}
	toolCall, _ := updates[1]["toolCall"].(map[string]any)
	if toolCall == nil {
		t.Fatalf("tool_call payload = %+v", updates[1])
	}
	if toolCall["toolCallId"] != "call-1" || toolCall["title"] != "read_file" ||
		toolCall["kind"] != "other" || toolCall["status"] != "in_progress" {
		t.Fatalf("tool_call = %+v", toolCall)
	}
	if _, ok := toolCall["rawInput"]; !ok {
		t.Fatalf("tool_call rawInput missing: %+v", toolCall)
	}

	if kind := updateKind(updates[2]); kind != "tool_call_update" {
		t.Fatalf("update[2] kind = %q, want tool_call_update; %+v", kind, updates[2])
	}
	if updates[2]["toolCallId"] != "call-1" || updates[2]["status"] != "completed" {
		t.Fatalf("tool_call_update = %+v", updates[2])
	}
	content, _ := updates[2]["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "content" {
		t.Fatalf("tool content = %+v", updates[2]["content"])
	}
	text := content[0].(map[string]any)["content"].(map[string]any)["text"]
	if text != "file contents" {
		t.Fatalf("tool content text = %v", text)
	}

	if kind := updateKind(updates[3]); kind != UpdateAgentMessageChunk {
		t.Fatalf("update[3] kind = %q", kind)
	}
	if text := updates[3]["content"].(map[string]any)["text"]; text != " Done reading." {
		t.Fatalf("last text chunk = %v", text)
	}
	if last := frames[len(frames)-1]; last.ID == nil || !strings.Contains(string(last.Result), "end_turn") {
		t.Fatalf("final frame = %+v, want end_turn result", last)
	}
}

// TestToolCallCarriesRawInput covers the verification finding that ACP tool
// notifications dropped the call arguments entirely: the read model now keeps
// the raw JSON and both the start and completion updates expose it.
func TestToolCallCarriesRawInput(t *testing.T) {
	chat := newFakeChat()
	call := contracts.ToolCall{ID: "call-9", Name: "read_file", Status: "running",
		Arguments: `{"path":"a.txt"}`}
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{call}, true)
		call.Status, call.Summary = "ok", "done"
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{call}, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service, promptLine(1, "sess-1", "read it"))
	updates := notificationUpdates(t, frames)
	if len(updates) != 2 {
		t.Fatalf("updates = %+v, want tool_call + tool_call_update", updates)
	}
	callPayload, _ := updates[0]["toolCall"].(map[string]any)
	if callPayload == nil {
		t.Fatalf("tool_call payload = %+v", updates[0])
	}
	raw, _ := callPayload["rawInput"].(map[string]any)
	if raw == nil || raw["path"] != "a.txt" {
		t.Fatalf("tool_call rawInput = %#v, want the decoded arguments", callPayload["rawInput"])
	}
	if updates[1]["title"] != "read_file" {
		t.Fatalf("tool_call_update title = %v, want the tool name", updates[1]["title"])
	}
	rawDone, _ := updates[1]["rawInput"].(map[string]any)
	if rawDone == nil || rawDone["path"] != "a.txt" {
		t.Fatalf("tool_call_update rawInput = %#v", updates[1]["rawInput"])
	}
}

func TestToolCallFailureStatus(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{ID: "call-9", Name: "bash", Status: "running"}}, true)
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{
			ID: "call-9", Name: "bash", Status: "error", Summary: "exit status 1",
		}}, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	updates := notificationUpdates(t, serveLines(t, service, promptLine(1, "sess-1", "run")))
	if len(updates) != 2 || updateKind(updates[1]) != "tool_call_update" {
		t.Fatalf("updates = %+v", updates)
	}
	if updates[1]["status"] != "failed" {
		t.Fatalf("failed status = %v", updates[1]["status"])
	}
	content := updates[1]["content"].([]any)
	if text := content[0].(map[string]any)["content"].(map[string]any)["text"]; text != "exit status 1" {
		t.Fatalf("failure content = %v", text)
	}
}

func TestToolCallContentTruncated(t *testing.T) {
	long := strings.Repeat("x", 5000)
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{ID: "call-2", Name: "read", Status: "running"}}, true)
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{
			ID: "call-2", Name: "read", Status: "ok", Summary: long,
		}}, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	updates := notificationUpdates(t, serveLines(t, service, promptLine(1, "sess-1", "read")))
	var text string
	for _, update := range updates {
		if updateKind(update) != "tool_call_update" {
			continue
		}
		content := update["content"].([]any)
		text = content[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	}
	if len([]byte(text)) > 4<<10 {
		t.Fatalf("truncated content = %d bytes, want <= 4KiB", len([]byte(text)))
	}
	if !strings.HasPrefix(text, "xxxx") {
		t.Fatalf("truncated content prefix = %q", text[:min(8, len(text))])
	}
}

func TestToolCallEmptyIDGenerated(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{Name: "no_id", Status: "running"}}, true)
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{
			Name: "no_id", Status: "ok", Summary: "done",
		}}, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	updates := notificationUpdates(t, serveLines(t, service, promptLine(1, "sess-1", "run")))
	if len(updates) != 2 {
		t.Fatalf("updates = %+v", updates)
	}
	up := updates[0]["toolCall"].(map[string]any)
	if up["toolCallId"] != "call-1" {
		t.Fatalf("generated id = %v, want call-1", up["toolCallId"])
	}
	if updates[1]["toolCallId"] != "call-1" {
		t.Fatalf("generated update id = %v, want call-1", updates[1]["toolCallId"])
	}
}

func TestToolCallEmptyResultOmitsContent(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{ID: "call-3", Name: "noop", Status: "running"}}, true)
		c.setAssistantTools(sessionID, "", []contracts.ToolCall{{ID: "call-3", Name: "noop", Status: "ok"}}, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	updates := notificationUpdates(t, serveLines(t, service, promptLine(1, "sess-1", "run")))
	if len(updates) != 2 || updateKind(updates[1]) != "tool_call_update" {
		t.Fatalf("updates = %+v", updates)
	}
	if _, present := updates[1]["content"]; present {
		t.Fatalf("empty result content present: %+v", updates[1])
	}
}

func TestToolCallHistoryNotReplayed(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTools(sessionID, "answer", nil, false)
	}
	// A session that already contains an old completed tool call must not replay
	// it when the next prompt streams.
	chat.setAssistantTools("sess-1", "old", []contracts.ToolCall{{
		ID: "old", Name: "old_tool", Status: "ok", Summary: "old",
	}}, false)
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service, promptLine(1, "sess-1", "again"))
	for _, update := range notificationUpdates(t, frames) {
		if kind := updateKind(update); kind == "tool_call" || kind == "tool_call_update" {
			t.Fatalf("old tool call replayed: %+v", update)
		}
	}
}
