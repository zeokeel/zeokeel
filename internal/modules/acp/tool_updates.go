package acp

// Streaming agent tool calls as ACP session/update notifications. The ACP
// server only sees the ChatService read model, so tool activity is derived from
// the per-message ToolCall list as it appears in session snapshots: a new entry
// emits tool_call (in_progress), a transition to a final status emits
// tool_call_update with the (truncated) result summary as text content. History
// that already existed when the prompt started is seeded and not replayed.

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// UpdateToolCall is the ACP session update kind for a started tool call.
	UpdateToolCall = "tool_call"
	// UpdateToolCallUpdate is the ACP session update kind for a tool result.
	UpdateToolCallUpdate = "tool_call_update"
	// toolKindOther is the only kind this server reports (the read model does
	// not classify tools).
	toolKindOther = "other"
	// maxToolContentBytes bounds the result text written into one update (4 KiB).
	maxToolContentBytes = 4 << 10
)

// toolEvent is one derived tool-call notification (start and/or result).
type toolEvent struct {
	ID      string
	Title   string
	Input   string
	Started bool
	Final   bool
	Failed  bool
	Content string
}

// seedToolState records the tool calls already present in a session so a new
// prompt does not replay old history.
func (state *promptState) seedToolState(snapshot contracts.ChatSession) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.seedToolsLocked(snapshot)
}

// observeToolEventsLocked derives the tool events of one snapshot. The caller
// holds state.mu.
func (state *promptState) observeToolEventsLocked(snapshot contracts.ChatSession) []toolEvent {
	if state.toolStates == nil {
		state.toolStates = map[string]string{}
	}
	if state.toolIDs == nil {
		state.toolIDs = map[string]string{}
	}
	var events []toolEvent
	for messageIndex, message := range snapshot.Messages {
		if message.Role != contracts.ChatRoleAssistant {
			continue
		}
		for toolIndex, tool := range message.Tools {
			key := toolStateKey(tool, messageIndex, toolIndex)
			id := state.toolIDs[key]
			if id == "" {
				id = strings.TrimSpace(tool.ID)
				if id == "" {
					state.generated++
					id = fmt.Sprintf("call-%d", state.generated)
				}
				state.toolIDs[key] = id
			}
			status := strings.TrimSpace(tool.Status)
			previous, seen := state.toolStates[key]
			final, failed := toolFinalStatus(status)
			event := toolEvent{ID: id, Title: strings.TrimSpace(tool.Name), Input: strings.TrimSpace(tool.Arguments)}
			if !seen {
				event.Started = true
				state.toolStates[key] = status
				if final {
					event.Final = true
					event.Failed = failed
					event.Content = truncateToolContent(tool.Summary)
					state.toolStates[key] = status
				}
			} else if final && !toolStatusIsFinal(previous) {
				event.Final = true
				event.Failed = failed
				event.Content = truncateToolContent(tool.Summary)
				state.toolStates[key] = status
			}
			if event.Started || event.Final {
				events = append(events, event)
			}
		}
	}
	return events
}

// seedToolsLocked records the existing tool calls without emitting events.
func (state *promptState) seedToolsLocked(snapshot contracts.ChatSession) {
	if state.toolIDs == nil {
		state.toolIDs = map[string]string{}
	}
	if state.toolStates == nil {
		state.toolStates = map[string]string{}
	}
	for messageIndex, message := range snapshot.Messages {
		if message.Role != contracts.ChatRoleAssistant {
			continue
		}
		for toolIndex, tool := range message.Tools {
			key := toolStateKey(tool, messageIndex, toolIndex)
			id := strings.TrimSpace(tool.ID)
			if id == "" {
				// Keep the generated-id namespace stable with later snapshots.
				id = state.toolIDs[key]
			}
			state.toolIDs[key] = id
			state.toolStates[key] = strings.TrimSpace(tool.Status)
		}
	}
}

// toolStateKey identifies one tool call across snapshots: the stable tool id
// when present, else its position in the read model.
func toolStateKey(tool contracts.ToolCall, messageIndex, toolIndex int) string {
	if id := strings.TrimSpace(tool.ID); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf("index:%d:%d", messageIndex, toolIndex)
}

// toolFinalStatus classifies a tool status: final reports a finished result and
// failed reports an error result.
func toolFinalStatus(status string) (final, failed bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "running", "in_progress", "pending", "started":
		return false, false
	case "error", "failed", "failure":
		return true, true
	default:
		return true, false
	}
}

// toolStatusIsFinal reports whether a recorded status is a final one.
func toolStatusIsFinal(status string) bool {
	final, _ := toolFinalStatus(status)
	return final
}

// truncateToolContent bounds result text to maxToolContentBytes bytes, keeping
// valid UTF-8 and reserving room for the ellipsis when truncation happens.
func truncateToolContent(content string) string {
	content = strings.TrimSpace(content)
	if len([]byte(content)) <= maxToolContentBytes {
		return content
	}
	limit := maxToolContentBytes - len("…")
	cut := content[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// writeToolEvent writes one derived tool-call notification for a prompt.
func (s *service) writeToolEvent(writer *frameWriter, sessionID string, event toolEvent) {
	if event.Started {
		writer.writeNotification(MethodSessionUpdate, map[string]any{
			"sessionId": sessionID,
			"update": map[string]any{
				"sessionUpdate": UpdateToolCall,
				"toolCall": map[string]any{
					"toolCallId": event.ID,
					"title":      event.Title,
					"kind":       toolKindOther,
					"status":     "in_progress",
					"rawInput":   toolRawInput(event.Input),
				},
			},
		})
	}
	if !event.Final {
		return
	}
	update := map[string]any{
		"sessionUpdate": UpdateToolCallUpdate,
		"toolCallId":    event.ID,
		"status":        "completed",
	}
	if event.Failed {
		update["status"] = "failed"
	}
	if event.Title != "" {
		update["title"] = event.Title
	}
	if event.Input != "" {
		update["rawInput"] = toolRawInput(event.Input)
	}
	if content := strings.TrimSpace(event.Content); content != "" {
		update["content"] = []any{map[string]any{
			"type":    "content",
			"content": map[string]any{"type": "text", "text": content},
		}}
	}
	writer.writeNotification(MethodSessionUpdate, map[string]any{
		"sessionId": sessionID,
		"update":    update,
	})
}

// toolRawInput renders a tool's raw JSON arguments as an ACP rawInput value:
// the decoded JSON value when the string is valid JSON, the original string
// otherwise, and "" when the provider reported no arguments.
func toolRawInput(input string) any {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		return input
	}
	return value
}
