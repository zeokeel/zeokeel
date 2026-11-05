package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// maxSSELine bounds one SSE line. Tool-call argument fragments can be
	// large, so this is far above the 64 KiB bufio.Scanner default.
	maxSSELine = 1 << 20
	// doneData is the terminal SSE payload defined by the OpenAI API.
	doneData = "[DONE]"
)

// toolCallAccumulator reassembles tool calls that a server streams in
// fragments. Each delta carries an index plus a slice of the function name or
// arguments; the complete call exists only once the stream ends.
type toolCallAccumulator struct {
	order []int
	calls map[int]*pendingToolCall
}

type pendingToolCall struct {
	id   string
	name string
	args strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{calls: make(map[int]*pendingToolCall)}
}

// add merges one delta's tool calls into the accumulator. Deltas arrive in
// index order in practice, but order is tracked explicitly so the assembled
// calls keep the model's ordering even if they do not.
func (a *toolCallAccumulator) add(deltas []streamToolCall) {
	for _, delta := range deltas {
		call, ok := a.calls[delta.Index]
		if !ok {
			call = &pendingToolCall{}
			a.calls[delta.Index] = call
			a.order = append(a.order, delta.Index)
		}
		// ID and name are normally sent whole in the first fragment only.
		if delta.ID != "" {
			call.id = delta.ID
		}
		if delta.Function.Name != "" {
			call.name = delta.Function.Name
		}
		call.args.WriteString(delta.Function.Arguments)
	}
}

// complete returns the assembled calls in index order, dropping fragments that
// never produced a name (those cannot be invoked). An empty argument string is
// normalized to "{}", the canonical "no arguments" object.
func (a *toolCallAccumulator) complete() []runtime.ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	calls := make([]runtime.ToolCall, 0, len(a.order))
	for _, index := range a.order {
		pending := a.calls[index]
		if pending == nil || pending.name == "" {
			continue
		}
		args := pending.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		calls = append(calls, runtime.ToolCall{
			ID:        pending.id,
			Name:      pending.name,
			Arguments: json.RawMessage(args),
		})
	}
	if len(calls) == 0 {
		return nil
	}
	return calls
}

// streamState carries the mutable state of one SSE response.
type streamState struct {
	ctx   context.Context
	out   chan<- runtime.Chunk
	calls *toolCallAccumulator
	usage *runtime.Usage
}

// readStream consumes body until the server ends the stream, emits chunks and
// returns. The caller closes out.
func (p *Provider) readStream(ctx context.Context, body io.Reader, out chan<- runtime.Chunk) {
	state := &streamState{
		ctx:   ctx,
		out:   out,
		calls: newToolCallAccumulator(),
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		// Blank lines separate events; comment lines start with ':' per the
		// SSE spec. Neither carries data.
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok || field != "data" {
			continue // event:, id:, retry: and malformed lines are ignored
		}
		payload := strings.TrimPrefix(value, " ")
		if payload == "" {
			continue
		}
		if payload == doneData {
			state.finish()
			return
		}
		if !state.handle(payload) {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			state.fail(ctxErr)
			return
		}
		state.fail(fmt.Errorf("openai: read stream: %w", err))
		return
	}
	// Some OpenAI-compatible servers close the connection without sending
	// [DONE]; treat a clean EOF as a normal end of stream.
	state.finish()
}

// handle decodes one SSE payload and emits its deltas. It returns false when
// the stream is finished or the consumer is gone. Malformed payloads are
// skipped: one bad event must not abort a whole response.
func (s *streamState) handle(payload string) bool {
	var event streamEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return true
	}
	if event.Error != nil {
		s.fail(fmt.Errorf("openai: stream error: %s", event.Error.text()))
		return false
	}

	for _, choice := range event.Choices {
		delta := choice.Delta
		// DeepSeek sends reasoning_content; some gateways use reasoning.
		reasoning := delta.ReasoningContent
		if reasoning == "" {
			reasoning = delta.Reasoning
		}
		if reasoning != "" && !s.send(runtime.Chunk{Reasoning: reasoning}) {
			return false
		}
		if delta.Content != "" && !s.send(runtime.Chunk{Text: delta.Content}) {
			return false
		}
		if len(delta.ToolCalls) > 0 {
			s.calls.add(delta.ToolCalls)
		}
	}
	if event.Usage != nil {
		s.usage = usageFromWire(event.Usage)
	}
	return true
}

// finish emits the terminal chunks in order: assembled tool calls, the usage
// report, then the Done marker. Calls are sent as one chunk so the runtime
// receives an atomic, fully assembled tool-call list.
func (s *streamState) finish() {
	if calls := s.calls.complete(); len(calls) > 0 {
		if !s.send(runtime.Chunk{ToolCalls: calls}) {
			return
		}
	}
	if s.usage != nil && !s.send(runtime.Chunk{Usage: s.usage}) {
		return
	}
	s.sendDone()
}

// usageFromWire translates the OpenAI usage object into runtime.Usage.
func usageFromWire(u *streamUsage) *runtime.Usage {
	if u == nil {
		return nil
	}
	out := &runtime.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		out.CachedInputTokens = u.PromptTokensDetails.CachedTokens
	} else if u.PromptCacheHitTokens > 0 {
		out.CachedInputTokens = u.PromptCacheHitTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return out
}

// send delivers one chunk, giving up when the request context is cancelled so
// the reader goroutine can never outlive its consumer.
func (s *streamState) send(chunk runtime.Chunk) bool {
	select {
	case s.out <- chunk:
		return true
	case <-s.ctx.Done():
		return false
	}
}

// sendDone delivers the terminal marker. It waits for buffer space unless the
// stream was cancelled.
func (s *streamState) sendDone() {
	select {
	case s.out <- runtime.Chunk{Done: true}:
	case <-s.ctx.Done():
	}
}

// fail reports err as the final chunk. It must not block a cancelled stream,
// but it still tries to hand the cause to a consumer that is draining.
func (s *streamState) fail(err error) {
	select {
	case s.out <- runtime.Chunk{Err: err}:
		return
	default:
	}
	select {
	case s.out <- runtime.Chunk{Err: err}:
	case <-s.ctx.Done():
	}
}
