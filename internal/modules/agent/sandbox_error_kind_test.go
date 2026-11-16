package agent

// RG-03 (2026-11 audit): rig v0.18.1 appends a "[sandbox: suspected OS sandbox
// denial …]" attribution line to shell/jobs results that matched a known
// denial dialect. A failed tool call carrying that line must surface the
// sandbox_denied kind on the tool card (live and restored), while ordinary
// failures keep their existing classification.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const sandboxAttributionLine = `[sandbox: suspected OS sandbox denial (matched "permission denied"; policy mode workspace-write, enforcement confined); the command ran inside the OS file sandbox]`

// driveToolFailure runs one scripted tool call + error result through
// handleEvent and returns the resulting tool call read model.
func driveToolFailure(t *testing.T, resultContent string) contracts.ToolCall {
	t.Helper()
	svc := newQueueService(t, nil, nil, nil)
	id, err := svc.NewSession("tool-error")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID: "m-a", Role: contracts.ChatRoleAssistant, Streaming: true,
	})
	state.assistant = len(state.messages) - 1
	svc.mu.Unlock()

	ctx := context.Background()
	call := &rig.ToolCall{ID: "call-1", Name: "bash", Arguments: `{"command":"cp x /etc/hosts"}`}
	svc.handleEvent(ctx, id, "", rig.Event{Type: rig.EventToolCall, Iteration: 1, Call: call}, nil)
	svc.handleEvent(ctx, id, "", rig.Event{
		Type: rig.EventToolResult, Iteration: 1, Call: call,
		Result: &rig.ToolResult{Content: resultContent, IsError: true},
	}, nil)
	session := mustSession(t, svc, id)
	if len(session.Messages[0].Tools) != 1 {
		t.Fatalf("tools = %+v, want one", session.Messages[0].Tools)
	}
	return session.Messages[0].Tools[0]
}

func TestToolResultSandboxAttributionKind(t *testing.T) {
	tool := driveToolFailure(t, "cp: cannot create regular file '/etc/hosts': Permission denied\n"+sandboxAttributionLine)
	if tool.Status != "error" {
		t.Fatalf("status = %q, want error", tool.Status)
	}
	if tool.ErrorKind != contracts.MessageErrorKindSandboxDenied {
		t.Fatalf("ErrorKind = %q, want %q (rig attribution line must classify as sandbox denial)",
			tool.ErrorKind, contracts.MessageErrorKindSandboxDenied)
	}
}

// Control: an ordinary failure without the attribution line is not misread as
// a sandbox denial, and a cancelled call carries no error kind.
func TestToolResultOrdinaryFailureNotSandbox(t *testing.T) {
	tool := driveToolFailure(t, "cp: cannot create regular file '/etc/hosts': Permission denied")
	if tool.Status != "error" || tool.ErrorKind != contracts.MessageErrorKindOther {
		t.Fatalf("ordinary failure = %q/%q, want error/other (no attribution, no guess)", tool.Status, tool.ErrorKind)
	}
	tool = driveToolFailure(t, "command exited with status 1")
	if tool.ErrorKind != contracts.MessageErrorKindOther {
		t.Fatalf("generic failure ErrorKind = %q, want other", tool.ErrorKind)
	}
}

// The attribution survives a restart: the durable tool_result event carries
// the kind (the bounded summary may cut the trailing attribution line off).
func TestToolResultSandboxKindRestored(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newQueueService(t, store, settings, nil)
	id, err := svc.NewSession("tool-error-restore")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// One durable assistant turn with a tool call, then the live events.
	if err := store.ReplaceHistory(context.Background(), id, []rig.Message{
		rig.NewMessage(rig.RoleUser, "copy the file"),
		{Role: rig.RoleAssistant, ToolCalls: []rig.ToolCall{{ID: "call-1", Name: "bash", Arguments: `{}`}}},
	}); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	svc.mu.Lock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID: "m-a", Role: contracts.ChatRoleAssistant, Streaming: true,
	})
	state.assistant = len(state.messages) - 1
	svc.mu.Unlock()
	ctx := context.Background()
	call := &rig.ToolCall{ID: "call-1", Name: "bash", Arguments: `{}`}
	svc.handleEvent(ctx, id, "", rig.Event{Type: rig.EventToolCall, Iteration: 1, Call: call}, nil)
	// A long output: the bounded summary keeps the head, the attribution line
	// at the tail is cut off — the persisted kind must carry it instead.
	longOutput := make([]byte, 5000)
	for i := range longOutput {
		longOutput[i] = 'x'
	}
	svc.handleEvent(ctx, id, "", rig.Event{
		Type: rig.EventToolResult, Iteration: 1, Call: call,
		Result: &rig.ToolResult{Content: string(longOutput) + "\n" + sandboxAttributionLine, IsError: true},
	}, nil)

	second := newQueueService(t, store, settings, nil)
	second.restore(context.Background())
	restored := mustSession(t, second, id)
	var tool *contracts.ToolCall
	for index := range restored.Messages {
		for toolIndex := range restored.Messages[index].Tools {
			if restored.Messages[index].Tools[toolIndex].ID == "call-1" {
				tool = &restored.Messages[index].Tools[toolIndex]
			}
		}
	}
	if tool == nil {
		t.Fatalf("restored tool call missing: %+v", restored.Messages)
	}
	if tool.Status != "error" || tool.ErrorKind != contracts.MessageErrorKindSandboxDenied {
		t.Fatalf("restored tool = %q/%q, want error/%q", tool.Status, tool.ErrorKind, contracts.MessageErrorKindSandboxDenied)
	}
}

// Legacy durable events without error_kind classify their content on hydration
// (an attribution line within the bounded summary still surfaces).
func TestToolResultLegacyEventClassifiesContent(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newQueueService(t, store, settings, nil)
	id, err := svc.NewSession("legacy-tool-error")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.ReplaceHistory(context.Background(), id, []rig.Message{
		rig.NewMessage(rig.RoleUser, "copy the file"),
		{Role: rig.RoleAssistant, ToolCalls: []rig.ToolCall{{ID: "call-1", Name: "bash", Arguments: `{}`}}},
	}); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	callPayload, _ := json.Marshal(map[string]any{"id": "call-1", "name": "bash", "arguments": "{}", "text_offset": 0})
	resultPayload, _ := json.Marshal(map[string]any{
		"id": "call-1", "name": "bash", "status": "error",
		"content": "Permission denied\n" + sandboxAttributionLine,
	})
	for _, event := range []rig.SessionEvent{
		{SessionID: id, Type: "tool_call", Time: time.Now(), Payload: callPayload},
		{SessionID: id, Type: "tool_result", Time: time.Now(), Payload: resultPayload},
	} {
		if err := store.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	second := newQueueService(t, store, settings, nil)
	second.restore(context.Background())
	restored := mustSession(t, second, id)
	var tool *contracts.ToolCall
	for index := range restored.Messages {
		for toolIndex := range restored.Messages[index].Tools {
			if restored.Messages[index].Tools[toolIndex].ID == "call-1" {
				tool = &restored.Messages[index].Tools[toolIndex]
			}
		}
	}
	if tool == nil {
		t.Fatalf("restored tool call missing: %+v", restored.Messages)
	}
	if tool.ErrorKind != contracts.MessageErrorKindSandboxDenied {
		t.Fatalf("legacy event ErrorKind = %q, want %q", tool.ErrorKind, contracts.MessageErrorKindSandboxDenied)
	}
}
