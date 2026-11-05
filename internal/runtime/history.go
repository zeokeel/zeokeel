package runtime

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// History persists a session's conversation across turns. An Agent reads prior
// messages with Messages before each turn and appends the turn's messages with
// Append after it completes successfully. A nil History makes the Agent
// stateless: each Run sees only the prompt it was given.
//
// Implementations are called from multiple sessions concurrently and must be
// safe for concurrent use. Messages are returned in chronological order; a
// session with no prior turns returns an empty slice and a nil error.
//
// A failed or cancelled turn is never appended, so a caller that wants to retry
// a turn starts from the last completed conversation state.
type History interface {
	Messages(ctx context.Context, sessionID string) ([]Message, error)
	Append(ctx context.Context, sessionID string, msgs ...Message) error
}

// messagesToEino converts stored history into the eino schema consumed by the
// ADK runner. Entries that cannot be represented (unknown roles, empty
// assistant placeholders, tool results without a call id) become eino
// validation errors on the API side, so they are dropped here.
func messagesToEino(msgs []Message) []*schema.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]*schema.Message, 0, len(msgs))
	for _, msg := range msgs {
		if converted := messageToEino(msg); converted != nil {
			out = append(out, converted)
		}
	}
	return out
}

func messageToEino(msg Message) *schema.Message {
	switch msg.Role {
	case RoleSystem:
		if msg.Content == "" {
			return nil
		}
		converted := schema.SystemMessage(msg.Content)
		if isCompactionSummary(msg) {
			// The app always configures a non-empty Instruction, and eino's
			// defaultGenModelInput drops a leading system message in that case
			// to avoid duplicating the stale instruction. A compaction summary
			// is not a stale instruction: it is the only carrier of the
			// compacted conversation, so it must survive that strip. Marking
			// it like a middleware system reminder is eino's supported way to
			// opt out (adk/chatmodel.go MessageExtraKeySystemReminder).
			converted.Extra = map[string]any{adk.MessageExtraKeySystemReminder: true}
		}
		return converted
	case RoleUser:
		if msg.Content == "" && len(msg.Images) == 0 {
			return nil
		}
		return userMessageWithAttachments(msg.Content, msg.Images)
	case RoleAssistant:
		// An assistant row with no visible text and no tool call is a draft
		// leftover (a stopped/crashed turn persisted only reasoning). Sending it
		// back to the model would inject an empty assistant turn, so drop it —
		// the reasoning is still visible in the trajectory.
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			return nil
		}
		converted := schema.AssistantMessage(msg.Content, toolCallsToEino(msg.ToolCalls))
		converted.ReasoningContent = msg.Reasoning
		return converted
	case RoleTool:
		if msg.ToolCallID == "" {
			return nil
		}
		opts := make([]schema.ToolMessageOption, 0, 1)
		if msg.ToolName != "" {
			opts = append(opts, schema.WithToolName(msg.ToolName))
		}
		return schema.ToolMessage(msg.Content, msg.ToolCallID, opts...)
	default:
		return nil
	}
}

// messageFromEino converts one eino message into the shape stored in History.
func messageFromEino(msg *schema.Message) Message {
	if msg == nil {
		return Message{}
	}
	return Message{
		Role:       roleFromEino(msg.Role),
		Content:    msg.Content,
		Reasoning:  msg.ReasoningContent,
		ToolCalls:  toolCallsFromEino(msg.ToolCalls),
		Images:     imagePartsFromEino(msg),
		ToolCallID: msg.ToolCallID,
		ToolName:   msg.ToolName,
	}
}

// empty reports whether msg carries nothing worth persisting. Assistant
// placeholders and empty tool results (which still carry a call id) are
// handled by the per-role checks above; this is the final guard for history.
func (m Message) empty() bool {
	return m.Content == "" &&
		m.Reasoning == "" &&
		len(m.ToolCalls) == 0 &&
		len(m.Images) == 0 &&
		m.ToolCallID == ""
}

// turnRecord accumulates the messages produced by one turn: the user prompt,
// every assistant message (including the ones that only carry tool calls), and
// every tool result. The run goroutine owns the record; tools never touch it.
type turnRecord struct {
	// mu guards msgs: the run goroutine appends while a concurrently running
	// tool (or parallel tool call) takes a barrier checkpoint snapshot.
	mu   sync.Mutex
	msgs []Message
}

func newTurnRecord(userText string) *turnRecord {
	return &turnRecord{msgs: []Message{{Role: RoleUser, Content: userText}}}
}

func (r *turnRecord) add(msg Message) {
	if r == nil || msg.empty() {
		return
	}
	r.mu.Lock()
	r.msgs = append(r.msgs, msg)
	r.mu.Unlock()
}

// messages returns a copy so callers cannot mutate the record's state.
func (r *turnRecord) messages() []Message {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return nil
	}
	return append([]Message(nil), r.msgs...)
}

// recordMessage appends msg to the current turn's record when ctx belongs to
// an Agent turn.
func recordMessage(ctx context.Context, msg *schema.Message) {
	if msg == nil {
		return
	}
	if tc := turnContextFromContext(ctx); tc != nil {
		tc.record.add(messageFromEino(msg))
	}
}
