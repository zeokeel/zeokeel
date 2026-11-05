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
// concurrent use: it holds no per-stream state.
type Provider struct {
	baseURL         string
	apiKey          string
	model           string
	client          *http.Client
	temperature     *float32
	maxTokens       int
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

	return &Provider{
		baseURL:         base,
		apiKey:          cfg.APIKey,
		model:           model,
		client:          client,
		temperature:     temperature,
		maxTokens:       cfg.MaxTokens,
		reasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
	}, nil
}

// Model reports the provider's default model.
func (p *Provider) Model() string { return p.model }

// BaseURL reports the normalized API root.
func (p *Provider) BaseURL() string { return p.baseURL }

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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", UserAgent)
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completions: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close() //nolint:errcheck // response body
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyMax))
		if body := strings.TrimSpace(string(snippet)); body != "" {
			return nil, fmt.Errorf("openai: chat completions: %s: %s", resp.Status, body)
		}
		return nil, fmt.Errorf("openai: chat completions: %s", resp.Status)
	}

	out := make(chan runtime.Chunk, chunkBuffer)
	go func() {
		defer close(out)
		defer resp.Body.Close() //nolint:errcheck // response body
		p.readStream(ctx, resp.Body, out)
	}()
	return out, nil
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
		ReasoningEffort: p.reasoningEffort,
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
