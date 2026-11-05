package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// newTestProvider starts a server with handler and returns a Provider pointed
// at its /v1 root.
func newTestProvider(t *testing.T, handler http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := New(Config{
		BaseURL: srv.URL + "/v1",
		APIKey:  "test-key",
		Model:   "default-model",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, srv
}

// ssePayload formats one OpenAI SSE data event.
func ssePayload(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal SSE payload: %v", err)
	}
	return "data: " + string(raw) + "\n\n"
}

// writeSSE writes payloads and flushes, failing the test on write errors.
func writeSSE(t *testing.T, w http.ResponseWriter, payloads ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, payload := range payloads {
		if _, err := io.WriteString(w, payload); err != nil {
			t.Errorf("write SSE payload: %v", err)
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// collect drains ch until it closes, failing on timeout.
func collect(t *testing.T, ch <-chan runtime.Chunk) []runtime.Chunk {
	t.Helper()
	var chunks []runtime.Chunk
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return chunks
			}
			chunks = append(chunks, c)
		case <-timeout:
			t.Fatal("timed out waiting for the chunk channel to close")
			return chunks
		}
	}
}

func textOf(chunks []runtime.Chunk) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Text)
	}
	return b.String()
}

func reasoningOf(chunks []runtime.Chunk) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Reasoning)
	}
	return b.String()
}

func TestStreamText(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/chat/completions"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		writeSSE(t, w,
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Hello"}}},
			}),
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": ", world"}}},
			}),
			"data: [DONE]\n\n",
		)
	})

	ch, err := p.Stream(context.Background(), runtime.Request{
		Model:    "test-model",
		Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks := collect(t, ch)
	if got, want := textOf(chunks), "Hello, world"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	for _, c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected chunk error: %v", c.Err)
		}
		if c.Reasoning != "" {
			t.Fatalf("unexpected reasoning: %q", c.Reasoning)
		}
	}
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Fatalf("last chunk = %+v, want Done", last)
	}
	if n := len(chunks); n > 0 {
		for i, c := range chunks[:n-1] {
			if c.Done {
				t.Fatalf("chunk %d marked Done before the last chunk", i)
			}
		}
	}
}

func TestStreamReasoningAndText(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": "think "}}},
			}),
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": "hard. "}}},
			}),
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "Answer."}}},
			}),
			"data: [DONE]\n\n",
		)
	})

	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks := collect(t, ch)
	if got, want := reasoningOf(chunks), "think hard. "; got != want {
		t.Fatalf("reasoning = %q, want %q", got, want)
	}
	if got, want := textOf(chunks), "Answer."; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if !chunks[len(chunks)-1].Done {
		t.Fatal("stream did not end with Done")
	}
}

func TestStreamSkipsMalformedData(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			"data: {not json\n\n",
			": keep-alive comment\n\n",
			"event: ping\n\n",
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "ok"}}},
			}),
			"data: [DONE]\n\n",
		)
	})

	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks := collect(t, ch)
	if got, want := textOf(chunks), "ok"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	for _, c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected chunk error: %v", c.Err)
		}
	}
}

func TestStreamUsage(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}}},
			}),
			ssePayload(t, map[string]any{
				"choices": []any{},
				"usage": map[string]any{
					"prompt_tokens":     11,
					"completion_tokens": 7,
					"total_tokens":      18,
					"prompt_tokens_details": map[string]any{
						"cached_tokens": 3,
					},
					"completion_tokens_details": map[string]any{
						"reasoning_tokens": 2,
					},
				},
			}),
			"data: [DONE]\n\n",
		)
	})

	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks := collect(t, ch)
	var usage *runtime.Usage
	for _, c := range chunks {
		if c.Usage != nil {
			usage = c.Usage
			if c.Text != "" || c.Reasoning != "" {
				t.Fatalf("usage chunk also carried text: %+v", c)
			}
		}
	}
	if usage == nil {
		t.Fatal("stream reported no usage")
	}
	want := runtime.Usage{
		InputTokens:       11,
		OutputTokens:      7,
		CachedInputTokens: 3,
		ReasoningTokens:   2,
		TotalTokens:       18,
	}
	if *usage != want {
		t.Fatalf("usage = %+v, want %+v", *usage, want)
	}
	if !chunks[len(chunks)-1].Done {
		t.Fatal("usage stream did not end with Done")
	}
}

// TestToolCallAccumulationAcrossChunks exercises the fragment assembly: the
// name and id arrive with the first delta and the JSON arguments are split
// across several later deltas.
func TestToolCallAccumulationAcrossChunks(t *testing.T) {
	acc := newToolCallAccumulator()
	acc.add([]streamToolCall{{
		Index:    0,
		ID:       "call_1",
		Type:     "function",
		Function: streamToolCallFn{Name: "read_file", Arguments: ""},
	}})
	acc.add([]streamToolCall{{Index: 0, Function: streamToolCallFn{Arguments: `{"path":`}}})
	acc.add([]streamToolCall{{Index: 0, Function: streamToolCallFn{Arguments: `"a.txt"}`}}})
	acc.add([]streamToolCall{{
		Index:    1,
		ID:       "call_2",
		Type:     "function",
		Function: streamToolCallFn{Name: "list_dir", Arguments: `{}`},
	}})

	calls := acc.complete()
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2: %+v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "read_file" {
		t.Fatalf("call 0 = %+v", calls[0])
	}
	if got, want := string(calls[0].Arguments), `{"path":"a.txt"}`; got != want {
		t.Fatalf("call 0 arguments = %q, want %q", got, want)
	}
	if calls[1].Name != "list_dir" || string(calls[1].Arguments) != "{}" {
		t.Fatalf("call 1 = %+v", calls[1])
	}

	// A stream with no tool calls must produce no calls.
	if extra := newToolCallAccumulator().complete(); extra != nil {
		t.Fatalf("empty accumulator returned %+v", extra)
	}
}

// TestToolCallAccumulationIgnoresNamelessCalls covers servers that stream an
// index with only an id and no function name: such a fragment cannot name a
// tool and is dropped.
func TestToolCallAccumulationIgnoresNamelessCalls(t *testing.T) {
	acc := newToolCallAccumulator()
	acc.add([]streamToolCall{{Index: 5, ID: "call_x", Function: streamToolCallFn{Arguments: "{}"}}})
	if calls := acc.complete(); calls != nil {
		t.Fatalf("nameless fragment produced %+v", calls)
	}
}

// TestStreamToolCallsEmitted drives tool-call fragments through a real SSE
// response and checks that the provider emits one chunk holding the complete
// calls, before usage and Done.
func TestStreamToolCallsEmitted(t *testing.T) {
	toolDelta := func(index int, id, name, args string) map[string]any {
		fn := map[string]any{"arguments": args}
		if name != "" {
			fn["name"] = name
		}
		call := map[string]any{"index": index, "function": fn}
		if id != "" {
			call["id"] = id
			call["type"] = "function"
		}
		return map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{call}}}},
		}
	}

	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			ssePayload(t, toolDelta(0, "call_1", "read_file", "")),
			ssePayload(t, toolDelta(0, "", "", `{"path":`)),
			ssePayload(t, toolDelta(0, "", "", `"a.txt"}`)),
			ssePayload(t, toolDelta(1, "call_2", "list_dir", `{}`)),
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
			}),
			ssePayload(t, map[string]any{
				"choices": []any{},
				"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9},
			}),
			"data: [DONE]\n\n",
		)
	})

	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks := collect(t, ch)

	var callChunks []runtime.Chunk
	for _, c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected chunk error: %v", c.Err)
		}
		if len(c.ToolCalls) > 0 {
			callChunks = append(callChunks, c)
		}
	}
	if len(callChunks) != 1 {
		t.Fatalf("got %d tool-call chunks, want 1: %+v", len(callChunks), callChunks)
	}
	calls := callChunks[0].ToolCalls
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2: %+v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "read_file" {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if got, want := string(calls[0].Arguments), `{"path":"a.txt"}`; got != want {
		t.Errorf("call 0 arguments = %q, want %q", got, want)
	}
	if calls[1].ID != "call_2" || calls[1].Name != "list_dir" || string(calls[1].Arguments) != "{}" {
		t.Errorf("call 1 = %+v", calls[1])
	}
	// The tool-call chunk must precede the usage report and Done.
	callAt, usageAt, doneAt := -1, -1, -1
	for i, c := range chunks {
		switch {
		case len(c.ToolCalls) > 0:
			callAt = i
		case c.Usage != nil:
			usageAt = i
		case c.Done:
			doneAt = i
		}
	}
	if usageAt < 0 || doneAt < 0 {
		t.Fatalf("missing usage (%d) or Done (%d) chunk: %+v", usageAt, doneAt, chunks)
	}
	if !(callAt < usageAt && usageAt < doneAt) {
		t.Fatalf("chunk order calls=%d usage=%d done=%d, want calls < usage < done", callAt, usageAt, doneAt)
	}
}

// TestUsageFromWireDeepSeekCache checks DeepSeek's flat prompt_cache_hit_tokens
// field, which stands in for OpenAI's nested cached_tokens.
func TestUsageFromWireDeepSeekCache(t *testing.T) {
	usage := usageFromWire(&streamUsage{
		PromptTokens:         20,
		CompletionTokens:     6,
		TotalTokens:          26,
		PromptCacheHitTokens: 12,
		CompletionTokensDetails: &completionTokenDetails{
			ReasoningTokens: 3,
		},
	})
	want := &runtime.Usage{
		InputTokens:       20,
		OutputTokens:      6,
		CachedInputTokens: 12,
		ReasoningTokens:   3,
		TotalTokens:       26,
	}
	if *usage != *want {
		t.Fatalf("usage = %+v, want %+v", *usage, want)
	}
	if usageFromWire(nil) != nil {
		t.Fatal("nil wire usage must map to nil")
	}
}

// TestStreamReasoningFallback covers gateways that spell the field
// "reasoning" instead of DeepSeek's "reasoning_content".
func TestStreamReasoningFallback(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning": "alt thought"}}},
			}),
			"data: [DONE]\n\n",
		)
	})
	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got, want := reasoningOf(collect(t, ch)), "alt thought"; got != want {
		t.Fatalf("reasoning = %q, want %q", got, want)
	}
}

// TestStreamStopsAtDone verifies that bytes after the [DONE] sentinel are
// ignored and that the channel closes.
func TestStreamStopsAtDone(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, ssePayload(t, map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "before"}}},
		})+"data: [DONE]\n\n")
		// A server that keeps writing after [DONE] must not be consumed.
		_, _ = io.WriteString(w, ssePayload(t, map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "after"}}},
		}))
	})
	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks := collect(t, ch)
	if got, want := textOf(chunks), "before"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if !chunks[len(chunks)-1].Done {
		t.Fatal("stream did not end with Done")
	}
}

// TestStreamBufferedChunks pushes more chunks than the channel buffer holds to
// prove the reader does not deadlock against a consumer that starts draining
// late.
func TestStreamBufferedChunks(t *testing.T) {
	const n = chunkBuffer * 3
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < n; i++ {
			if _, err := io.WriteString(w, ssePayload(t, map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "x"}}},
			})); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Drain after the server has had a chance to fill the buffer.
	time.Sleep(50 * time.Millisecond)
	chunks := collect(t, ch)
	if got := len(textOf(chunks)); got != n {
		t.Fatalf("received %d text chunks, want %d", got, n)
	}
}

func TestStreamNonSuccessStatus(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad model"}}`)
	})

	ch, err := p.Stream(context.Background(), runtime.Request{})
	if err == nil {
		t.Fatalf("Stream returned nil error, channel = %v", ch)
	}
	for _, want := range []string{"400", "bad model"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestStreamCancelMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, ssePayload(t, map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "first"}}},
		}))
		// Hold the response open until the client aborts it.
		<-r.Context().Done()
	})

	ch, err := p.Stream(ctx, runtime.Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	select {
	case c := <-ch:
		if c.Text != "first" {
			t.Fatalf("first chunk = %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first chunk")
	}

	cancel()

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for range ch {
		}
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not close after context cancellation")
	}
}

func TestStreamRequestShape(t *testing.T) {
	type captured struct {
		body    map[string]any
		headers http.Header
		method  string
		path    string
	}
	got := make(chan captured, 1)

	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request body %q: %v", raw, err)
			return
		}
		got <- captured{body: body, headers: r.Header.Clone(), method: r.Method, path: r.URL.Path}
		writeSSE(t, w, "data: [DONE]\n\n")
	})

	temperature := float32(0.2)
	p.temperature = &temperature
	p.maxTokens = 128

	_, err := p.Stream(context.Background(), runtime.Request{
		Model: "test-model",
		Messages: []runtime.Message{
			{Role: runtime.RoleSystem, Content: "be brief"},
			{Role: runtime.RoleUser, Content: "read a.txt"},
			{Role: runtime.RoleAssistant, ToolCalls: []runtime.ToolCall{{
				ID:        "call_1",
				Name:      "read_file",
				Arguments: json.RawMessage(`{"path":"a.txt"}`),
			}}},
			{Role: runtime.RoleTool, Content: "contents", ToolCallID: "call_1", ToolName: "read_file"},
		},
		Tools: []runtime.ToolSpec{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var req captured
	select {
	case req = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive the request")
	}

	if req.method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.method)
	}
	if req.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", req.path)
	}
	if auth := req.headers.Get("Authorization"); auth != "Bearer test-key" {
		t.Errorf("Authorization = %q", auth)
	}
	if ua := req.headers.Get("User-Agent"); ua != UserAgent {
		t.Errorf("User-Agent = %q, want %q", ua, UserAgent)
	}
	if ct := req.headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if accept := req.headers.Get("Accept"); accept != "text/event-stream" {
		t.Errorf("Accept = %q", accept)
	}

	body := req.body
	if model := body["model"]; model != "test-model" {
		t.Errorf("model = %v, want test-model (Request.Model overrides Config.Model)", model)
	}
	if stream, ok := body["stream"].(bool); !ok || !stream {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	opts, ok := body["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage true", body["stream_options"])
	}
	if temp, ok := body["temperature"].(float64); !ok || temp != 0.2 {
		t.Errorf("temperature = %v, want 0.2", body["temperature"])
	}
	if max, ok := body["max_tokens"].(float64); !ok || max != 128 {
		t.Errorf("max_tokens = %v, want 128", body["max_tokens"])
	}

	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 4 {
		t.Fatalf("messages = %v, want 4 entries", body["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("message 0 = %v", first)
	}
	assistant := msgs[2].(map[string]any)
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant tool_calls = %v", assistant["tool_calls"])
	}
	call := calls[0].(map[string]any)
	if call["id"] != "call_1" || call["type"] != "function" {
		t.Errorf("tool call = %v", call)
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "read_file" || fn["arguments"] != `{"path":"a.txt"}` {
		t.Errorf("function = %v", fn)
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool message = %v", toolMsg)
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
	def := tools[0].(map[string]any)
	if def["type"] != "function" {
		t.Errorf("tool type = %v", def["type"])
	}
	schema := def["function"].(map[string]any)
	if schema["name"] != "read_file" || schema["description"] != "Read a file" {
		t.Errorf("tool function = %v", schema)
	}
	params, ok := schema["parameters"].(map[string]any)
	if !ok || params["type"] != "object" {
		t.Errorf("parameters = %v", schema["parameters"])
	}
}

func TestStreamDefaultsModel(t *testing.T) {
	var got map[string]any
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		writeSSE(t, w, "data: [DONE]\n\n")
	})

	if _, err := p.Stream(context.Background(), runtime.Request{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if model := got["model"]; model != "default-model" {
		t.Fatalf("model = %v, want the configured default", model)
	}
	for _, field := range []string{"temperature", "max_tokens", "tools"} {
		if _, ok := got[field]; ok {
			t.Errorf("%s = %v, want the field omitted when unset", field, got[field])
		}
	}
}

func TestStreamInvalidToolSchema(t *testing.T) {
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be contacted for an invalid request")
	})
	_, err := p.Stream(context.Background(), runtime.Request{
		Tools: []runtime.ToolSpec{{Name: "bad", Parameters: json.RawMessage(`{`)}},
	})
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("error = %v, want a schema error naming the tool", err)
	}
}

func TestNewDefaults(t *testing.T) {
	p, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.BaseURL() != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", p.BaseURL(), DefaultBaseURL)
	}
	if p.Model() != DefaultModel {
		t.Errorf("Model = %q, want %q", p.Model(), DefaultModel)
	}
	if p.client == nil || p.client.Timeout != DefaultTimeout {
		t.Errorf("client timeout = %v, want %v", p.client, DefaultTimeout)
	}

	// A supplied client is used as-is.
	custom := &http.Client{Timeout: time.Second}
	p, err = New(Config{HTTPClient: custom, BaseURL: "https://example.com/v1/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.client != custom {
		t.Error("custom HTTP client was replaced")
	}
	if p.BaseURL() != "https://example.com/v1" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", p.BaseURL())
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"scheme", Config{BaseURL: "ftp://example.com/v1"}},
		{"no host", Config{BaseURL: "https:///v1"}},
		{"negative max tokens", Config{MaxTokens: -1}},
		{"negative temperature", Config{Temperature: ptr(float32(-0.5))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Fatalf("New(%+v) returned no error", tt.cfg)
			}
		})
	}
}

func TestNewTrimsDefaultBaseURLSlash(t *testing.T) {
	p, err := New(Config{BaseURL: "https://api.deepseek.com/v1/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.BaseURL() != "https://api.deepseek.com/v1" {
		t.Fatalf("BaseURL = %q", p.BaseURL())
	}
}

func TestStreamTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening any more

	p, err := New(Config{BaseURL: url + "/v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Stream(context.Background(), runtime.Request{})
	if err == nil {
		t.Fatal("Stream returned no error for a dead endpoint")
	}
}

func ptr[T any](v T) *T { return &v }

// TestReasoningEffortReachesRequest proves the model settings page's
// reasoning_effort is actually serialized on the wire request.
func TestReasoningEffortReachesRequest(t *testing.T) {
	var got map[string]any
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		close(done)
		writeSSE(t, w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n",
			"data: [DONE]\n\n")
	}))
	defer srv.Close()
	p, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m", ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), runtime.Request{
		Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	collect(t, ch)
	<-done
	if effort, _ := got["reasoning_effort"].(string); effort != "high" {
		t.Fatalf("request reasoning_effort = %v, want high (body: %+v)", got["reasoning_effort"], got)
	}
}
