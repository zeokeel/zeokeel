package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/zeokeel/zeokeel/internal/state"
)

// Compile-time guarantees that the Provider adapter is usable anywhere eino
// accepts a chat model.
var (
	_ model.ChatModel            = (*providerChatModel)(nil)
	_ model.ToolCallingChatModel = (*providerChatModel)(nil)
)

// providerChatModel adapts ZeoKeel's Provider abstraction to the eino
// model.ChatModel contract. ChatModelAgent talks eino schema.Message; our
// providers talk Request/Chunk. This type is the only place where the two
// vocabularies meet for model output.
type providerChatModel struct {
	provider Provider
	model    string

	mu    sync.Mutex
	tools []*schema.ToolInfo
}

func newProviderChatModel(p Provider, modelName string) *providerChatModel {
	return &providerChatModel{provider: p, model: modelName}
}

// Generate satisfies model.BaseModel by materializing the streaming response.
func (m *providerChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	stream, err := m.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var chunks []*schema.Message
	for {
		msg, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if msg != nil {
			chunks = append(chunks, msg)
		}
	}
	if len(chunks) == 0 {
		return schema.AssistantMessage("", nil), nil
	}
	msg, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("runtime: concat provider chunks: %w", err)
	}
	return msg, nil
}

// Stream satisfies model.BaseModel. It opens the provider stream eagerly so
// setup errors surface synchronously, then forwards text/reasoning/usage
// chunks as eino assistant message chunks.
func (m *providerChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	req := m.buildRequest(ctx, input, opts...)

	// Barrier checkpoint: persist the request context-before it reaches the
	// provider so a crash during the response cannot lose the request or the
	// work before it. The checkpoint store call is cheap for the no-store /
	// no-turn cases because saveBarrierCheckpoint returns immediately.
	if err := saveBarrierCheckpoint(ctx, CheckpointPhaseRequested); err != nil {
		return nil, fmt.Errorf("runtime: save checkpoint before model request: %w", err)
	}

	chunks, err := m.provider.Stream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("runtime: provider stream: %w", err)
	}
	if chunks == nil {
		return nil, fmt.Errorf("runtime: provider returned nil chunk channel")
	}

	tc := turnContextFromContext(ctx)
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer sw.Close()
		if tc != nil {
			// Count the call (a step) and raise soft budget advisories before
			// the stream closes: eino builds the next request only after it
			// sees EOF, so anything queued here rides that request.
			defer tc.noteModelCall()
		}
		for {
			select {
			case <-ctx.Done():
				sw.Send(nil, ctx.Err())
				return
			case c, ok := <-chunks:
				if !ok {
					return
				}
				if c.Err != nil {
					sw.Send(nil, fmt.Errorf("runtime: provider chunk: %w", c.Err))
					return
				}
				if tc != nil {
					// Track usage and identical repeated calls synchronously
					// while converting, before eino can act on the stream.
					if c.Usage != nil {
						tc.noteUsageSample(c.Usage)
					}
					for _, call := range c.ToolCalls {
						tc.noteToolCall(call.Name, call.Arguments)
					}
				}
				msg := chunkToMessage(c)
				last := c.Done
				// Drop empty keepalive chunks so eino's concat never sees a
				// nil message. A Done-only chunk becomes the final empty
				// assistant message that terminates the stream.
				if msg == nil {
					if !last {
						continue
					}
					msg = &schema.Message{Role: schema.Assistant}
				}
				if sw.Send(msg, nil) {
					return
				}
				if last {
					return
				}
			}
		}
	}()
	return sr, nil
}

// BindTools implements the deprecated model.ChatModel mutator.
func (m *providerChatModel) BindTools(tools []*schema.ToolInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = tools
	return nil
}

// WithTools implements model.ToolCallingChatModel without mutating the
// receiver, so one base model can serve sessions with different tool sets.
func (m *providerChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &providerChatModel{provider: m.provider, model: m.model, tools: tools}, nil
}

func (m *providerChatModel) buildRequest(ctx context.Context, input []*schema.Message, opts ...model.Option) Request {
	common := model.GetCommonOptions(nil, opts...)

	m.mu.Lock()
	bound := m.tools
	m.mu.Unlock()
	tools := common.Tools
	if tools == nil {
		tools = bound
	}

	messages := messagesFromEino(injectToolImages(input))
	// Soft advisories (cost budget crossings, repeated-tool reminders) are
	// queued on the turn context and delivered with the next request. They ride
	// the latest tool result, which is where the model naturally reads the
	// outcome it is about to act on.
	if tc := turnContextFromContext(ctx); tc != nil {
		var leftover []string
		messages, leftover = injectAdvisories(messages, tc.takeAdvisories())
		// A plain chat request has no tool result to attach the notice to; put
		// it back so the next request (the usual tool loop) still carries it
		// instead of silently dropping it.
		for _, advisory := range leftover {
			tc.enqueueAdvisory(advisory)
		}
	}
	req := Request{
		Model:    m.model,
		Messages: messages,
		Tools:    toolSpecs(tools),
	}
	if sessionID, ok := ctx.Value(sessionIDContextKey{}).(string); ok {
		req.SessionID = sessionID
	}
	return req
}

// injectAdvisories appends the queued advisory texts to the latest tool result
// in the request, so the model reads them next to the result they concern. When
// the request has no tool result yet (a plain chat request) the advisories are
// left for the next model call: they are only useful inside a tool loop.
func injectAdvisories(messages []Message, advisories []string) ([]Message, []string) {
	if len(messages) == 0 || len(advisories) == 0 {
		return messages, nil
	}
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleTool {
			last = i
			break
		}
	}
	if last < 0 {
		return messages, advisories
	}
	var b strings.Builder
	b.WriteString(messages[last].Content)
	for _, advisory := range advisories {
		if advisory == "" {
			continue
		}
		b.WriteString("\n\n[harness advisory] ")
		b.WriteString(advisory)
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	out[last].Content = b.String()
	return out, nil
}

// chunkToMessage translates one provider chunk into an eino assistant message
// chunk. Tool calls are forwarded as complete calls so eino's tools node can
// execute them; an assistant stream with tool calls terminates the model turn
// and the ADK feeds the tool results back on the next model call.
func chunkToMessage(c Chunk) *schema.Message {
	if c.Text == "" && c.Reasoning == "" && c.Usage == nil && len(c.ToolCalls) == 0 {
		return nil
	}
	msg := &schema.Message{
		Role:             schema.Assistant,
		Content:          c.Text,
		ReasoningContent: c.Reasoning,
		ToolCalls:        toolCallsToEino(c.ToolCalls),
	}
	if c.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{Usage: usageToEino(c.Usage)}
	}
	return msg
}

// toolCallsToEino converts ZeoKeel tool calls to eino's shape. Index is left
// nil deliberately: a chunk carries whole calls, and nil indexes make eino's
// ConcatMessages append calls from separate chunks instead of merging them into
// one.
func toolCallsToEino(calls []ToolCall) []schema.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]schema.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, schema.ToolCall{
			ID:   call.ID,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      call.Name,
				Arguments: string(call.Arguments),
			},
		})
	}
	return out
}

func usageToEino(u *Usage) *schema.TokenUsage {
	if u == nil {
		return nil
	}
	return &schema.TokenUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		PromptTokenDetails: schema.PromptTokenDetails{
			CachedTokens: u.CachedInputTokens,
		},
		CompletionTokensDetails: schema.CompletionTokensDetails{
			ReasoningTokens: u.ReasoningTokens,
		},
	}
}

func usageFromEino(u *schema.TokenUsage) *Usage {
	if u == nil {
		return nil
	}
	return &Usage{
		InputTokens:       u.PromptTokens,
		OutputTokens:      u.CompletionTokens,
		CachedInputTokens: u.PromptTokenDetails.CachedTokens,
		ReasoningTokens:   u.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:       u.TotalTokens,
	}
}

func messagesFromEino(input []*schema.Message) []Message {
	out := make([]Message, 0, len(input))
	for _, msg := range input {
		if msg == nil {
			continue
		}
		out = append(out, Message{
			Role:       roleFromEino(msg.Role),
			Content:    msg.Content,
			Reasoning:  msg.ReasoningContent,
			ToolCalls:  toolCallsFromEino(msg.ToolCalls),
			Images:     imagePartsFromEino(msg),
			ToolCallID: msg.ToolCallID,
			ToolName:   msg.ToolName,
		})
	}
	return out
}

func roleFromEino(role schema.RoleType) Role {
	switch role {
	case schema.User:
		return RoleUser
	case schema.Assistant:
		return RoleAssistant
	case schema.Tool:
		return RoleTool
	case schema.System:
		return RoleSystem
	default:
		return Role(role)
	}
}

func toolCallsFromEino(calls []schema.ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	return out
}

func toolSpecs(infos []*schema.ToolInfo) []ToolSpec {
	if len(infos) == 0 {
		return nil
	}
	out := make([]ToolSpec, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			continue
		}
		spec := ToolSpec{Name: info.Name, Description: info.Desc}
		if info.ParamsOneOf != nil {
			if js, err := info.ParamsOneOf.ToJSONSchema(); err == nil && js != nil {
				if raw, err := json.Marshal(js); err == nil {
					spec.Parameters = raw
				}
			}
		}
		out = append(out, spec)
	}
	return out
}

// einoInvokableTool adapts a ZeoKeel Tool to eino's InvokableTool so the ADK
// tools node can execute it. When a Gate is configured it also enforces
// approval: the call is announced as state.ApprovalRequest and blocks until the
// user decides. When a ToolReviewer is configured, a call that would ask is
// first reviewed by the model: ALLOW skips the prompt; DENY asks the user with
// the reviewer's reason attached, so the user can override the recommendation;
// a reviewer failure falls back to the ordinary prompt.
type einoInvokableTool struct {
	tool     Tool
	info     *schema.ToolInfo
	gate     Gate
	reviewer ToolReviewer
}

var _ tool.InvokableTool = (*einoInvokableTool)(nil)

func newEinoTool(t Tool, gate Gate, reviewer ToolReviewer) *einoInvokableTool {
	return &einoInvokableTool{
		tool:     t,
		gate:     gate,
		reviewer: reviewer,
		info: &schema.ToolInfo{
			Name: t.Name(),
			Desc: t.Description(),
		},
	}
}

func (e *einoInvokableTool) Info(context.Context) (*schema.ToolInfo, error) {
	return e.info, nil
}

func (e *einoInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	args := json.RawMessage(argumentsInJSON)
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return e.toolFailure(ctx, "", "", fmt.Errorf("tool %q: invalid JSON arguments %q", e.tool.Name(), argumentsInJSON))
	}
	if err := e.awaitApproval(ctx, args); err != nil {
		return e.toolFailure(ctx, "", "", err)
	}
	// Barrier checkpoint: a top-level tool with external side effects records
	// that it is about to be dispatched before its body runs. A crash in the
	// body then leaves an unknown-result call rather than an unrecorded side
	// effect. A checkpoint failure fails closed: the tool body never runs.
	if checkpointBarrierTool(e.tool.Name()) {
		if err := saveBarrierCheckpoint(ctx, CheckpointPhaseToolPending); err != nil {
			return "", fmt.Errorf("runtime: save checkpoint before tool %q: %w", e.tool.Name(), err)
		}
	}
	text, err := e.tool.Invoke(ctx, args)
	if err != nil {
		return e.toolFailure(ctx, compose.GetToolCallID(ctx), text, err)
	}
	return text, nil
}

// maxToolErrorRunes bounds the error marker appended to a tool result. A
// failing command can produce a long message; the model only needs enough to
// understand the failure, and the UI card keeps the same bounded text.
const maxToolErrorRunes = 2000

// toolFailure delivers a tool error to the model as the call's result instead
// of returning it to eino's tools node. The node discards the result and fails
// the whole turn on a non-nil error, which used to hide bash's output, a
// missing file's path and a denied approval behind a fatal [NodeRunError].
// The turn sink still records the failure so state.ToolEnd reports IsError for
// the red card and the tool_calls table. Context cancellation stays an error:
// a stopped turn must not keep running.
func (e *einoInvokableTool) toolFailure(ctx context.Context, callID, text string, err error) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if callID == "" {
		callID = compose.GetToolCallID(ctx)
	}
	if tc := turnContextFromContext(ctx); tc != nil {
		tc.markToolFailure(callID)
	}
	marker := "[tool error] " + err.Error()
	if runes := []rune(marker); len(runes) > maxToolErrorRunes {
		marker = string(runes[:maxToolErrorRunes]) + "…"
	}
	if text == "" {
		return marker, nil
	}
	return strings.TrimRight(text, "\n") + "\n" + marker, nil
}

// awaitApproval runs automatic review for a call the Gate requires approval
// for, then publishes the approval request and blocks until the Gate resolves
// it. The eino tools node passes the tool call id through ctx, which is what
// correlates the request event with the user's decision.
func (e *einoInvokableTool) awaitApproval(ctx context.Context, args json.RawMessage) error {
	if e.gate == nil || !e.gate.RequireApproval(e.tool.Name(), args) {
		return nil
	}
	// A delegated run without a turn context has no approval card and no user
	// request to authorize the action. Fail closed before the automatic
	// reviewer runs: a review with empty authorization evidence must not be
	// able to auto-allow a gated call.
	if turnContextFromContext(ctx) == nil && delegatedApprovalFromContext(ctx) {
		return fmt.Errorf("runtime: tool %q needs user approval, but this delegated agent has no approval channel. %s",
			e.tool.Name(), delegatedApprovalGuidance)
	}
	callID := compose.GetToolCallID(ctx)
	if callID == "" {
		// A tool invoked outside the ADK tools node (for example by a future
		// middleware) has no call id; mint one so the decision can still be
		// correlated with the event the UI renders.
		callID = uuid.NewString()
	}
	if allow, reason, reviewed := reviewToolCall(ctx, e.reviewer, e.tool.Name(), args); reviewed {
		if allow {
			// The review decided the call may run: no ApprovalRequest is
			// published and the tool executes immediately.
			return nil
		}
		// The reviewer recommends denial, not a final rejection (DSH's ask
		// policy): ask the user, carrying the reviewer's reason so the approval
		// card can explain why. The user's decision wins either way.
		return awaitGateApproval(ctx, e.gate, callID, e.tool.Name(), args, reason)
	}
	return awaitGateApproval(ctx, e.gate, callID, e.tool.Name(), args, "")
}

// awaitGateApproval publishes the ApprovalRequest for one call and blocks until
// the Gate resolves it. It is shared by the eino tool wrapper and any nested
// tool dispatcher, so approvals behave identically in both paths.
// reviewReason, when non-empty, is the automatic reviewer's recommendation to
// deny; the request still asks the user to decide, and the shell shows the
// reason on the approval card.
func awaitGateApproval(ctx context.Context, gate Gate, callID, name string, args json.RawMessage, reviewReason string) error {
	tc := turnContextFromContext(ctx)
	if tc == nil || tc.sink == nil {
		if delegatedApprovalFromContext(ctx) {
			return fmt.Errorf("runtime: tool %q needs user approval, but this delegated agent has no approval channel. %s",
				name, delegatedApprovalGuidance)
		}
		return fmt.Errorf("runtime: tool %q requires approval outside an agent turn", name)
	}
	request := state.ApprovalRequest{
		CallID:       callID,
		Name:         name,
		Args:         args,
		Kind:         state.ApprovalOther,
		ReviewReason: strings.TrimSpace(reviewReason),
	}
	if !tc.sink.send(ctx, request) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("runtime: tool %q: approval request for call %q was dropped", name, callID)
	}
	allow, err := gate.Wait(ctx, callID)
	if err != nil {
		return fmt.Errorf("runtime: tool %q call %q: %w", name, callID, err)
	}
	if !allow {
		return fmt.Errorf("runtime: tool %q call %q denied by user", name, callID)
	}
	return nil
}
