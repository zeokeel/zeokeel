// Package openai implements runtime.Provider against any OpenAI-compatible
// chat-completions endpoint: DeepSeek, OpenAI, or a local server (llama.cpp,
// vLLM, Ollama's OpenAI shim).
//
// The provider speaks the streaming chat-completions protocol: it POSTs
// {BaseURL}/chat/completions with stream=true, decodes the SSE response, and
// translates content, reasoning_content, tool calls and usage into
// runtime.Chunk values.
//
// Construction is deliberately small:
//
//	p, err := openai.New(openai.Config{
//		BaseURL: "https://api.deepseek.com/v1",
//		APIKey:  key,
//		Model:   "deepseek-chat",
//	})
//	agent, err := runtime.New(runtime.Config{Provider: p, Tools: reg})
//
// Network errors, non-2xx responses and request-encoding failures surface
// synchronously from Stream; stream-time failures arrive on the chunk channel
// as a Chunk with Err set, after which the channel is closed.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// DefaultBaseURL is the DeepSeek OpenAI-compatible API root.
	DefaultBaseURL = "https://api.deepseek.com/v1"
	// DefaultModel is DeepSeek's general chat model. runtime.Request.Model
	// overrides it per call.
	DefaultModel = "deepseek-chat"
	// DefaultResponseHeaderTimeout bounds the wait for response headers.
	// The streamed body has no overall deadline by default: long reasoning
	// generations legitimately stream past any fixed request cap, and the
	// caller's context (the Stop button) remains the bound.
	DefaultResponseHeaderTimeout = 60 * time.Second
	// UserAgent identifies ZeoKeel to the endpoint.
	UserAgent = "zeokeel"

	// chunkBuffer decouples the SSE reader from a consumer that briefly
	// stalls; it is small enough that cancellation is always prompt.
	chunkBuffer = 32
	// errorBodyMax caps how much of a failed response body is quoted.
	errorBodyMax = 512
)

// Config configures a Provider.
type Config struct {
	// BaseURL is the API root, without a trailing /chat/completions. Empty
	// means DefaultBaseURL.
	BaseURL string
	// APIKey is sent as a bearer token. It may be empty for local servers.
	APIKey string
	// Model is the default model. runtime.Request.Model overrides it.
	Model string
	// HTTPClient is used for requests. Nil means a client with Timeout and
	// DefaultResponseHeaderTimeout.
	HTTPClient *http.Client
	// Timeout bounds the whole HTTP response including the streamed body.
	// Zero means no overall request deadline (the default): the caller's
	// context and the Stop button bound a long generation, and only the
	// response header wait is capped. A positive value caps one request.
	Timeout time.Duration
	// StreamIdleTimeout bounds how long one streamed read may stay silent.
	// Zero means DefaultStreamIdleTimeout (300s, DSH's streamIdleTimeoutMs
	// default); a negative value disables the idle watchdog.
	StreamIdleTimeout time.Duration
	// Retry configures transient request recovery. Nil uses
	// DefaultRetryPolicy (DSH normal mode: five retries, 500ms..10s backoff,
	// 10% jitter); a non-nil policy with MaxRetries 0 disables retries.
	Retry *RetryPolicy
	// Temperature, when non-nil, is sent on every request. A pointer keeps
	// an explicit 0 distinct from "unset".
	Temperature *float32
	// MaxTokens, when positive, is sent on every request. runtime.Request
	// has no per-request sampling fields, so these are provider defaults.
	MaxTokens int
	// ReasoningEffort, when set, is sent as the request's reasoning_effort
	// (low/medium/high). The model settings page persists it and the app
	// passes it here, so changing it affects the next request.
	ReasoningEffort string
}

// Provider is an OpenAI-compatible runtime.Provider. It is safe for
// concurrent use: it holds no per-stream state. ReasoningEffort is the one
// mutable setting (the composer's per-session reasoning-strength menu changes
// it between requests), guarded by effortMu.
type Provider struct {
	baseURL     string
	apiKey      string
	model       string
	client      *http.Client
	temperature *float32
	maxTokens   int
	retry       RetryPolicy
	streamIdle  time.Duration

	effortMu        sync.RWMutex
	reasoningEffort string
}

var _ runtime.Provider = (*Provider)(nil)

// New validates cfg, applies defaults and returns a ready Provider.
func New(cfg Config) (*Provider, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("openai: invalid base URL %q: %w", cfg.BaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("openai: invalid base URL %q: scheme must be http or https", cfg.BaseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("openai: invalid base URL %q: missing host", cfg.BaseURL)
	}
	if cfg.MaxTokens < 0 {
		return nil, fmt.Errorf("openai: invalid max tokens %d: must be >= 0", cfg.MaxTokens)
	}
	if cfg.Temperature != nil && *cfg.Temperature < 0 {
		return nil, fmt.Errorf("openai: invalid temperature %v: must be >= 0", *cfg.Temperature)
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultModel
	}
	// Copy the pointed-to temperature so later mutation of cfg cannot
	// silently change the provider's defaults.
	var temperature *float32
	if cfg.Temperature != nil {
		value := *cfg.Temperature
		temperature = &value
	}
	client := cfg.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if transport.ResponseHeaderTimeout == 0 {
			transport.ResponseHeaderTimeout = DefaultResponseHeaderTimeout
		}
		client = &http.Client{Transport: transport, Timeout: cfg.Timeout}
	}
	retry := DefaultRetryPolicy
	if cfg.Retry != nil {
		retry = cfg.Retry.normalize()
	}
	streamIdle := cfg.StreamIdleTimeout
	switch {
	case streamIdle == 0:
		streamIdle = DefaultStreamIdleTimeout
	case streamIdle < 0:
		streamIdle = 0
	}

	return &Provider{
		baseURL:         base,
		apiKey:          cfg.APIKey,
		model:           model,
		client:          client,
		temperature:     temperature,
		maxTokens:       cfg.MaxTokens,
		reasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
		retry:           retry,
		streamIdle:      streamIdle,
	}, nil
}

// Model reports the provider's default model.
func (p *Provider) Model() string { return p.model }

// BaseURL reports the normalized API root.
func (p *Provider) BaseURL() string { return p.baseURL }

// SetReasoningEffort replaces the reasoning_effort sent on subsequent
// requests. The composer's per-session strength menu calls it so a change
// reaches the next request without rebuilding the provider; an empty effort
// omits the field and leaves the provider's own default.
func (p *Provider) SetReasoningEffort(effort string) {
	p.effortMu.Lock()
	p.reasoningEffort = strings.TrimSpace(effort)
	p.effortMu.Unlock()
}

// ReasoningEffort reports the effort the next request sends ("" omits the
// field).
func (p *Provider) ReasoningEffort() string {
	p.effortMu.RLock()
	defer p.effortMu.RUnlock()
	return p.reasoningEffort
}

// Stream starts a streaming completion. It returns once the server has
// accepted the request; the channel then carries chunks until the stream
// ends. A non-2xx response, an unreachable endpoint or an unencodable
// request is returned as a synchronous error.
//
// The channel closes after a Done chunk on success and after an Err chunk on
// failure. Cancelling ctx aborts the request, stops the reader goroutine and
// closes the channel without a terminal chunk, as the runtime consumes ctx
// cancellation itself.
func (p *Provider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	attemptsLeft := p.retry.MaxRetries
	resp, reqCtx, cancel, err := p.openResponse(ctx, payload, &attemptsLeft, 0)
	if err != nil {
		return nil, err
	}
	out := make(chan runtime.Chunk, chunkBuffer)
	go p.streamAttempts(ctx, payload, resp, reqCtx, cancel, out, attemptsLeft)
	return out, nil
}

// openResponse performs the request phase: build, send, and validate the HTTP
// response. Transient transport errors and retryable status codes (408/425/429/
// 5xx) are retried with the configured bounded backoff; a provider Retry-After
// header replaces the local delay. Non-retryable failures are returned
// synchronously, preserving the Provider contract for e.g. auth/validation
// errors. Each retry consumes one attempt from attemptsLeft.
func (p *Provider) openResponse(ctx context.Context, payload []byte, attemptsLeft *int, attempt int) (*http.Response, context.Context, context.CancelFunc, error) {
	for {
		reqCtx, cancel := context.WithCancel(ctx)
		httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			cancel()
			return nil, nil, nil, fmt.Errorf("openai: build request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("User-Agent", UserAgent)
		if p.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
		}

		resp, err := p.client.Do(httpReq)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return nil, nil, nil, requestFailure(ctx.Err())
			}
			if *attemptsLeft <= 0 || !retryableStreamError(err) {
				return nil, nil, nil, requestFailure(err)
			}
			*attemptsLeft--
			delay := p.delayFor(attempt, "")
			logRetry(p.retry.MaxRetries-*attemptsLeft, p.retry.MaxRetries, delay, err)
			attempt++
			if !sleepCtx(ctx, delay) {
				return nil, nil, nil, requestFailure(ctx.Err())
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			return resp, reqCtx, cancel, nil
		}
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyMax))
		_ = resp.Body.Close()
		cancel()
		body := strings.TrimSpace(string(snippet))
		var reqErr error
		if body != "" {
			reqErr = fmt.Errorf("openai: chat completions: %s: %s", resp.Status, body)
		} else {
			reqErr = fmt.Errorf("openai: chat completions: %s", resp.Status)
		}
		if *attemptsLeft <= 0 || !retryableStatus(resp.StatusCode) {
			return nil, nil, nil, reqErr
		}
		*attemptsLeft--
		delay := p.delayFor(attempt, resp.Header.Get("Retry-After"))
		logRetry(p.retry.MaxRetries-*attemptsLeft, p.retry.MaxRetries, delay, reqErr)
		attempt++
		if !sleepCtx(ctx, delay) {
			return nil, nil, nil, requestFailure(ctx.Err())
		}
	}
}

// streamAttempts reads an established response and retries failures that
// happened before any visible chunk was forwarded: read-phase transport errors,
// stream idle timeouts and empty responses. A failure after visible output is
// final because the transcript cannot retract the partial answer; this is the
// safe subset of DSH's step-boundary retry for ZeoKeel's streaming UI.
func (p *Provider) streamAttempts(ctx context.Context, payload []byte, resp *http.Response, reqCtx context.Context, cancel context.CancelFunc, out chan<- runtime.Chunk, attemptsLeft int) {
	defer close(out)
	attempt := 0
	for {
		if resp == nil {
			var err error
			resp, reqCtx, cancel, err = p.openResponse(ctx, payload, &attemptsLeft, attempt)
			if err != nil {
				out <- runtime.Chunk{Err: err}
				return
			}
		}
		attemptCh := make(chan runtime.Chunk, chunkBuffer)
		var body io.Reader = resp.Body
		var idle *idleReadWatcher
		if p.streamIdle > 0 {
			// The watchdog cancels the request context so the blocked body
			// read unblocks immediately; readStream reports the idle timeout
			// instead of the derived cancellation.
			idle = newIdleReadWatcher(resp.Body, p.streamIdle, cancel)
			body = idle
		}
		go func() {
			p.readStream(reqCtx, body, attemptCh, idle)
			close(attemptCh)
		}()

		emitted := false
		sawDone := false
		var failure error
		for c := range attemptCh {
			switch {
			case c.Err != nil:
				failure = c.Err
			case c.Done:
				sawDone = true
			default:
				emitted = true
				select {
				case out <- c:
				case <-ctx.Done():
					cancel()
					_ = resp.Body.Close()
					if idle != nil {
						idle.stop()
					}
					return
				}
			}
		}
		if idle != nil {
			idle.stop()
		}
		cancel()
		_ = resp.Body.Close()

		if ctx.Err() != nil {
			return
		}
		if failure == nil {
			if sawDone {
				// DSH's normal retry mode includes EMPTY_RESPONSE: a clean
				// completion with no text/reasoning/tool calls is retried
				// while budget remains, then passed through as empty.
				if !emitted && attemptsLeft > 0 {
					attemptsLeft--
					delay := p.delayFor(attempt, "")
					logRetry(p.retry.MaxRetries-attemptsLeft, p.retry.MaxRetries, delay, fmt.Errorf("empty response"))
					attempt++
					if !sleepCtx(ctx, delay) {
						return
					}
					resp = nil
					continue
				}
				out <- runtime.Chunk{Done: true}
				return
			}
			if emitted {
				// Content was already forwarded: retrying would duplicate it.
				out <- runtime.Chunk{Err: fmt.Errorf("openai: stream ended without a terminal event")}
				return
			}
			if attemptsLeft > 0 {
				// The stream closed before any visible output and without a
				// terminal Done marker (unexpected EOF/transport close): safe
				// to retry the whole request once per remaining attempt.
				attemptsLeft--
				delay := p.delayFor(attempt, "")
				logRetry(p.retry.MaxRetries-attemptsLeft, p.retry.MaxRetries, delay, fmt.Errorf("empty response"))
				attempt++
				if !sleepCtx(ctx, delay) {
					return
				}
				resp = nil
				continue
			}
			out <- runtime.Chunk{Done: true}
			return
		}
		if !emitted && attemptsLeft > 0 && retryableStreamError(failure) {
			attemptsLeft--
			delay := p.delayFor(attempt, "")
			logRetry(p.retry.MaxRetries-attemptsLeft, p.retry.MaxRetries, delay, failure)
			attempt++
			if !sleepCtx(ctx, delay) {
				return
			}
			resp = nil
			continue
		}
		out <- runtime.Chunk{Err: failure}
		return
	}
}

// buildRequest encodes req as an OpenAI chat-completions body.
func (p *Provider) buildRequest(req runtime.Request) ([]byte, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.model
	}

	messages := make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := chatMessage{
			Role:       string(m.Role),
			Content:    chatContentFor(m),
			ToolCallID: m.ToolCallID,
		}
		for _, call := range m.ToolCalls {
			args := strings.TrimSpace(string(call.Arguments))
			if args == "" {
				// OpenAI requires a JSON string; an empty object is the
				// canonical "no arguments".
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, chatToolCall{
				ID:   call.ID,
				Type: "function",
				Function: chatFunctionCall{
					Name:      call.Name,
					Arguments: args,
				},
			})
		}
		messages = append(messages, msg)
	}

	tools := make([]chatTool, 0, len(req.Tools))
	for _, spec := range req.Tools {
		if spec.Name == "" {
			return nil, fmt.Errorf("openai: tool with empty name")
		}
		fn := chatFunction{Name: spec.Name, Description: spec.Description}
		if len(spec.Parameters) > 0 {
			if !json.Valid(spec.Parameters) {
				return nil, fmt.Errorf("openai: tool %q: parameters are not valid JSON", spec.Name)
			}
			fn.Parameters = spec.Parameters
		}
		tools = append(tools, chatTool{Type: "function", Function: fn})
	}

	body := chatRequest{
		Model:           model,
		Messages:        messages,
		Tools:           tools,
		Stream:          true,
		StreamOptions:   &streamOptions{IncludeUsage: true},
		Temperature:     p.temperature,
		MaxTokens:       p.maxTokens,
		ReasoningEffort: p.ReasoningEffort(),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}
	return raw, nil
}

// chatContentFor maps a runtime message's content. Text-only messages and
// non-user/assistant roles (tool, system) keep the plain string form; a
// user/assistant message with at least one image becomes an OpenAI content
// array: one text part followed by one image_url part per image. An empty image
// URL is skipped, and if no usable image remains the string form is kept.
func chatContentFor(m runtime.Message) chatContent {
	content := chatContent{text: m.Content}
	if len(m.Images) == 0 || (m.Role != runtime.RoleUser && m.Role != runtime.RoleAssistant) {
		return content
	}
	parts := make([]chatContentPart, 0, 1+len(m.Images))
	text := m.Content
	parts = append(parts, chatContentPart{Type: "text", Text: &text})
	for _, image := range m.Images {
		if image.URL == "" {
			continue
		}
		detail := image.Detail
		if detail == "" {
			detail = "auto"
		}
		parts = append(parts, chatContentPart{
			Type:     "image_url",
			ImageURL: &chatImageURL{URL: image.URL, Detail: detail},
		})
	}
	if len(parts) == 1 {
		return content
	}
	content.parts = parts
	return content
}
