package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// emitFunc publishes one event. It returns false when the turn's context is
// done and the consumer should stop producing.
type emitFunc func(state.Event) bool

// turnUsage accumulates token accounting across the model calls of one turn.
// Providers that report no usage leave the counters at zero; nothing is
// fabricated.
type turnUsage struct {
	in    int
	out   int
	cache int
	steps int
}

// finishCall records one completed model call. usage may be nil.
func (u *turnUsage) finishCall(usage *schema.TokenUsage) {
	u.steps++
	if usage == nil {
		return
	}
	u.in += usage.PromptTokens
	u.out += usage.CompletionTokens
	u.cache += usage.PromptTokenDetails.CachedTokens
}

func (u *turnUsage) snapshot() state.Usage {
	return state.Usage{
		Turns:    1,
		Steps:    u.steps,
		In:       u.in,
		Out:      u.out,
		CacheHit: u.cache,
	}
}

// consumeMessage translates one eino AgentEvent message output into state
// events. Tool executions are echoed as ToolStart/ToolEnd so the UI can render
// cards without knowing about eino. pinned is the topology this turn started
// with; tool-call validation uses its mode and strictness, never the live ones,
// so a mode switch during the turn cannot invalidate the tools it can run.
func (a *Agent) consumeMessage(
	ctx context.Context,
	sessionID string,
	msgID string,
	pinned *agentState,
	mv *adk.MessageVariant,
	emit emitFunc,
	usage *turnUsage,
	pending map[string]time.Time,
	started map[string]bool,
) error {
	if mv == nil {
		return nil
	}

	if mv.IsStreaming {
		if mv.Role == schema.Tool {
			return consumeToolStream(ctx, mv, emit, pending)
		}
		return a.consumeAssistantStream(ctx, sessionID, msgID, pinned, mv, emit, usage, pending, started)
	}

	if mv.Message == nil {
		return nil
	}

	switch mv.Role {
	case schema.Tool:
		emitToolResult(ctx, mv.Message, emit, pending)
	case schema.Assistant:
		if mv.Message.ReasoningContent != "" {
			if !emit(state.Reasoning{SessionID: sessionID, MsgID: msgID, Text: mv.Message.ReasoningContent}) {
				return ctx.Err()
			}
		}
		if mv.Message.Content != "" {
			if !emit(state.Token{SessionID: sessionID, MsgID: msgID, Text: mv.Message.Content}) {
				return ctx.Err()
			}
		}
		if err := a.handleToolCalls(mv.Message.ToolCalls, pinned, emit, pending, started); err != nil {
			return err
		}
		recordMessage(ctx, mv.Message)
		var usagePtr *schema.TokenUsage
		if mv.Message.ResponseMeta != nil {
			usagePtr = mv.Message.ResponseMeta.Usage
		}
		usage.finishCall(usagePtr)
	}
	return nil
}

// consumeAssistantStream drains one streamed assistant message.
func (a *Agent) consumeAssistantStream(
	ctx context.Context,
	sessionID string,
	msgID string,
	pinned *agentState,
	mv *adk.MessageVariant,
	emit emitFunc,
	usage *turnUsage,
	pending map[string]time.Time,
	started map[string]bool,
) error {
	stream := mv.MessageStream
	defer stream.Close()

	var chunks []*schema.Message
	var lastUsage *schema.TokenUsage
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("runtime: assistant stream: %w", err)
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if chunk.ReasoningContent != "" {
			if !emit(state.Reasoning{SessionID: sessionID, MsgID: msgID, Text: chunk.ReasoningContent}) {
				return ctx.Err()
			}
		}
		if chunk.Content != "" {
			if !emit(state.Token{SessionID: sessionID, MsgID: msgID, Text: chunk.Content}) {
				return ctx.Err()
			}
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			lastUsage = chunk.ResponseMeta.Usage
		}
		if err := a.handleToolCalls(chunk.ToolCalls, pinned, emit, pending, started); err != nil {
			return err
		}
	}
	if len(chunks) > 0 {
		// Concatenate the streamed assistant chunks for history: eino merges
		// tool-call fragments and reasoning the same way the tools node did.
		if merged, err := schema.ConcatMessages(chunks); err == nil {
			recordMessage(ctx, merged)
		}
	}
	usage.finishCall(lastUsage)
	return nil
}

// handleToolCalls announces model-requested tool calls. It fails fast for
// unknown tools instead of relying on eino's tools node: with an empty tool
// list eino silently drops the call, and the runtime must always surface an
// error event for a call it cannot execute. Names resolve against the pinned
// mode and strictness, so a mode switch after the turn started cannot reject a
// tool the pinned topology still runs (or accept one it cannot).
func (a *Agent) handleToolCalls(
	calls []schema.ToolCall,
	pinned *agentState,
	emit emitFunc,
	pending map[string]time.Time,
	started map[string]bool,
) error {
	for _, call := range calls {
		if call.Function.Name == "" {
			// Streaming deltas may carry only an index; the fragment that has a
			// name is the one that can be resolved and announced.
			continue
		}
		if _, ok := a.toolByName(pinned.snap.mode, call.Function.Name); !ok && pinned.strict {
			return fmt.Errorf("runtime: model requested unknown tool %q", call.Function.Name)
		}
		if call.ID == "" {
			// The fragment has no call id yet, so it cannot be correlated with
			// a tool result; announce it on a later fragment.
			continue
		}
		if started[call.ID] {
			continue
		}
		args := json.RawMessage(call.Function.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = nil
		}
		started[call.ID] = true
		pending[call.ID] = time.Now()
		if !emit(state.ToolStart{CallID: call.ID, Name: call.Function.Name, Args: args}) {
			return context.Canceled
		}
	}
	return nil
}

// toolCallFailed consumes the adapter's failure mark for callID. The adapter
// converts a tool error into a normal tool result so the model can react; the
// mark keeps the UI and the tool_calls table reporting the error status.
func toolCallFailed(ctx context.Context, callID string) bool {
	tc := turnContextFromContext(ctx)
	return tc != nil && tc.takeToolFailure(callID)
}

// emitToolResult reports a completed (non-streaming) tool call and records the
// result for history.
func emitToolResult(ctx context.Context, msg *schema.Message, emit emitFunc, pending map[string]time.Time) {
	callID := msg.ToolCallID
	var duration time.Duration
	if startedAt, ok := pending[callID]; ok {
		duration = time.Since(startedAt)
		delete(pending, callID)
	}
	emit(state.ToolEnd{
		CallID:   callID,
		Result:   msg.Content,
		IsError:  toolCallFailed(ctx, callID),
		Duration: duration,
	})
	recordMessage(ctx, msg)
}

// consumeToolStream drains a streamed tool result (streamable tools are not
// used by the current Tool interface, but a middleware or future tool may
// stream; the result is surfaced as one ToolEnd at EOF).
func consumeToolStream(ctx context.Context, mv *adk.MessageVariant, emit emitFunc, pending map[string]time.Time) error {
	stream := mv.MessageStream
	defer stream.Close()

	var text strings.Builder
	var callID string
	var toolName string
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("runtime: tool stream: %w", err)
		}
		if chunk == nil {
			continue
		}
		text.WriteString(chunk.Content)
		if chunk.ToolCallID != "" {
			callID = chunk.ToolCallID
		}
		if chunk.ToolName != "" {
			toolName = chunk.ToolName
		}
	}
	var duration time.Duration
	if startedAt, ok := pending[callID]; ok {
		duration = time.Since(startedAt)
		delete(pending, callID)
	}
	emit(state.ToolEnd{
		CallID:   callID,
		Result:   text.String(),
		IsError:  toolCallFailed(ctx, callID),
		Duration: duration,
	})
	recordMessage(ctx, &schema.Message{
		Role:       schema.Tool,
		Content:    text.String(),
		ToolCallID: callID,
		ToolName:   toolName,
	})
	return nil
}
