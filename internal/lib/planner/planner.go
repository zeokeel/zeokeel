// Package planner adapts an OpenAI-compatible chat endpoint to
// contracts.PlannerModel: one prompt in, one raw answer out.
//
// It is the wire half of the Lead's triage pass. internal/lib/leadplan owns
// the prompt and the answer parser; this package owns only the HTTP hop, so the
// same code runs against a hosted provider, a local llama-server, or the
// acceptance fake model without importing a provider SDK. Configuration is
// injected by the assembly layer: in the product the endpoint and credential
// are the chat.baseURL / chat.apiKey settings rows and the model is chat.model.
//
// Failures are typed because the caller's reaction differs: an unauthorized
// answer usually means a bad key (retrying will not help), rate limiting means
// back off, and everything else is transient and worth retrying on the next
// pass. No error message ever carries the API key, and an endpoint that echoes
// the Authorization header back has that echo redacted.
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The planner's defaults. Timeout bounds one Decide call; MaxTokens bounds the
// answer (a JSON object is small, and a runaway answer is a bug); MaxPromptRunes
// is the prompt budget that keeps one oversized inbox item from becoming an
// expensive failure.
const (
	// DefaultTimeout is the per-call deadline when Config.Timeout is zero.
	DefaultTimeout = 60 * time.Second
	// DefaultMaxTokens is the completion cap when Config.MaxTokens is zero.
	DefaultMaxTokens = 512
	// DefaultMaxPromptRunes is the prompt budget when Config.MaxPromptRunes is
	// zero. leadplan's prompt is a few thousand characters at most; anything
	// past this is a digest or a bug.
	DefaultMaxPromptRunes = 12000
)

const (
	// systemPrompt is the fixed role line. It is part of the contract with the
	// model (leadplan's prompt carries the actual instructions and rules), and
	// the tests assert it literally.
	systemPrompt = "you are a triage planner that replies with a single JSON object"
	// chatCompletionsPath is appended to the configured base URL.
	chatCompletionsPath = "/chat/completions"
	// maxResponseBytes bounds the answer we are willing to read from an
	// endpoint. A triage answer is far below this; the cap exists so a broken
	// endpoint cannot make the gateway buffer an unbounded body.
	maxResponseBytes = 1 << 20
	// maxErrorSnippetRunes bounds the endpoint's error text that is copied into
	// our error. Enough to explain a refusal, not enough to fill a log.
	maxErrorSnippetRunes = 200
)

// The planner's typed failures. Callers tell them apart with errors.Is because
// the reaction differs: ErrPlannerUnauthorized is a configuration problem,
// ErrPlannerRateLimited is a "come back later", ErrPlannerUnavailable is a
// transient transport/endpoint failure, ErrPlannerEmpty is a well-formed but
// useless answer, ErrPlannerPromptTooLong is our own budget refusal (no call
// was made), and ErrPlannerInvalidConfig is a construction failure.
var (
	// ErrPlannerUnauthorized is returned for 401 and 403: the credential is
	// missing, wrong, or not allowed to use this model.
	ErrPlannerUnauthorized = errors.New("planner: endpoint rejected the credential")
	// ErrPlannerRateLimited is returned for 429.
	ErrPlannerRateLimited = errors.New("planner: rate limited")
	// ErrPlannerUnavailable is returned for any other status, a transport
	// failure, a cancelled call, or an answer that is not the expected JSON.
	ErrPlannerUnavailable = errors.New("planner: model unavailable")
	// ErrPlannerEmpty is returned when the endpoint answered successfully but
	// carried no text.
	ErrPlannerEmpty = errors.New("planner: empty model answer")
	// ErrPlannerPromptTooLong is returned before any network call when the
	// prompt exceeds Config.MaxPromptRunes.
	ErrPlannerPromptTooLong = errors.New("planner: prompt too long")
	// ErrPlannerInvalidConfig is returned by New for an unusable configuration.
	ErrPlannerInvalidConfig = errors.New("planner: invalid config")
)

// Compile-time proof that the injected seam is satisfied.
var _ contracts.PlannerModel = (*Planner)(nil)

// Config is the planner's typed configuration. APIKey is never serialized: it
// is a secret, and configuration documents are not.
type Config struct {
	// BaseURL is the OpenAI-compatible root, with or without a trailing slash
	// and with or without the /v1 suffix: "/chat/completions" is appended to it.
	BaseURL string `json:"baseURL"`
	// APIKey is the bearer credential. Empty is allowed (a local endpoint may
	// not need one) — the header is then omitted.
	APIKey string `json:"-"`
	// Model is the model identifier sent in the request body.
	Model string `json:"model"`
	// Timeout bounds one Decide call (zero uses DefaultTimeout). It is a Go
	// duration: when this struct is decoded from JSON the value is nanoseconds,
	// so assembly code should set it explicitly rather than in a document.
	Timeout time.Duration `json:"timeout,omitempty"`
	// MaxTokens caps the completion (zero uses DefaultMaxTokens).
	MaxTokens int `json:"maxTokens,omitempty"`
	// MaxPromptRunes caps the prompt in Unicode code points (zero uses
	// DefaultMaxPromptRunes). Counting runes, not bytes, keeps the budget honest
	// for non-Latin inbox items.
	MaxPromptRunes int `json:"maxPromptRunes,omitempty"`
	// Client is an optional transport (zero uses a fresh http.Client with no
	// client-level timeout, because each call carries its own deadline).
	Client *http.Client `json:"-"`
}

// Planner is the contracts.PlannerModel implementation: a thin, typed client
// for one chat-completions endpoint. It holds no mutable state, so one value is
// safe to share.
type Planner struct {
	endpoint       string
	apiKey         string
	model          string
	timeout        time.Duration
	maxTokens      int
	maxPromptRunes int
	client         *http.Client
}

// New validates cfg and returns a ready planner. A BaseURL that is not an
// absolute http(s) URL and an empty Model are rejected: both fail every call
// anyway, and failing at construction names the bad configuration once instead
// of on every triage pass.
func New(cfg Config) (*Planner, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("%w: baseURL is required", ErrPlannerInvalidConfig)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: baseURL %q is not a URL: %v", ErrPlannerInvalidConfig, endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: baseURL %q must be an absolute http or https URL", ErrPlannerInvalidConfig, endpoint)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: baseURL %q has no host", ErrPlannerInvalidConfig, endpoint)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("%w: model is required", ErrPlannerInvalidConfig)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	maxPromptRunes := cfg.MaxPromptRunes
	if maxPromptRunes <= 0 {
		maxPromptRunes = DefaultMaxPromptRunes
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}

	return &Planner{
		endpoint:       endpoint,
		apiKey:         strings.TrimSpace(cfg.APIKey),
		model:          model,
		timeout:        timeout,
		maxTokens:      maxTokens,
		maxPromptRunes: maxPromptRunes,
		client:         client,
	}, nil
}

// chatMessage is one message of the OpenAI chat shape.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the request body. Temperature is part of the wire contract, so
// it is a plain field: triage must be as deterministic as the endpoint allows.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

// chatResponse is the answer body. Only the first choice is used, and both
// shapes are accepted: message.content (chat-completions) and text
// (completions). Everything else in the envelope is ignored on purpose.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Text string `json:"text"`
	} `json:"choices"`
}

// Decide implements contracts.PlannerModel. It sends the prompt and returns the
// endpoint's answer verbatim (trimmed). The prompt budget and the context are
// checked before any network call, so an oversized prompt or a cancelled pass
// costs nothing.
func (p *Planner) Decide(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		// A cancelled call is the caller's own withdrawal, not an endpoint
		// fault; it is still classified as transient so callers have one
		// branch for "no answer this round".
		return "", fmt.Errorf("%w: %w", ErrPlannerUnavailable, err)
	}
	if runes := utf8.RuneCountInString(prompt); runes > p.maxPromptRunes {
		return "", fmt.Errorf("%w: prompt is %d characters, the budget is %d",
			ErrPlannerPromptTooLong, runes, p.maxPromptRunes)
	}

	payload, err := json.Marshal(chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Temperature: 0,
		MaxTokens:   p.maxTokens,
	})
	if err != nil {
		// Unreachable for this struct; kept so a future field cannot turn a
		// panic into a crash of the triage pass.
		return "", fmt.Errorf("%w: encode request: %v", ErrPlannerUnavailable, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.endpoint+chatCompletionsPath, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%w: build request: %w", ErrPlannerUnavailable, err)
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		// Transport errors carry the method and URL, never headers, so the key
		// cannot appear here; the chain is preserved so errors.Is still sees
		// context.Canceled / context.DeadlineExceeded.
		return "", fmt.Errorf("%w: %w", ErrPlannerUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("%w: read response: %w", ErrPlannerUnavailable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", p.statusError(resp, body)
	}

	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("%w: decode response: %v", ErrPlannerUnavailable, err)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("%w: the answer carries no choices", ErrPlannerEmpty)
	}
	choice := decoded.Choices[0]
	answer := strings.TrimSpace(choice.Message.Content)
	if answer == "" {
		answer = strings.TrimSpace(choice.Text)
	}
	if answer == "" {
		return "", fmt.Errorf("%w: the first choice carries no text", ErrPlannerEmpty)
	}
	return answer, nil
}

// statusError maps a non-2xx answer onto the typed failures, keeping a bounded,
// redacted snippet of the endpoint's own explanation.
func (p *Planner) statusError(resp *http.Response, body []byte) error {
	detail := p.redact(snippet(body))
	retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if retryAfter != "" {
		retryAfter = " (retry after " + retryAfter + ")"
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: status %d%s%s", ErrPlannerUnauthorized, resp.StatusCode, retryAfter, detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: status %d%s%s", ErrPlannerRateLimited, resp.StatusCode, retryAfter, detail)
	default:
		return fmt.Errorf("%w: status %d%s%s", ErrPlannerUnavailable, resp.StatusCode, retryAfter, detail)
	}
}

// redact removes the credential from text that came from outside before it is
// copied into an error message. An endpoint that echoes the Authorization
// header (or a proxy that logs it into its error page) must not be able to
// write the key into our logs.
func (p *Planner) redact(text string) string {
	if p.apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, p.apiKey, "[redacted]")
}

// snippet renders an endpoint error body as one short line: ": <text>", or ""
// when the body was empty.
func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if text == "" {
		return ""
	}
	if runes := []rune(text); len(runes) > maxErrorSnippetRunes {
		text = string(runes[:maxErrorSnippetRunes]) + "…"
	}
	return ": " + text
}
