// Package websearch implements a configurable HTTP web search tool.
//
// The searcher speaks a deliberately small, provider-neutral JSON protocol:
//
//	POST <endpoint>
//	Content-Type: application/json
//	Authorization: Bearer <api key>        (only when APIKey is set)
//	{"query":"...","count":5}
//
//	200 OK
//	{"results":[{"title":"...","url":"https://...","snippet":"..."}]}
//
// Any provider that can be fronted by that shape works; an adapter/proxy is
// needed for APIs whose request or response bodies differ (for example the
// OpenAI/DeepSeek search APIs, which return model-style content rather than a
// result array). Keeping one generic contract avoids guessing at provider
// payloads and keeps this package dependency-free.
//
// The tool is a runtime.Tool named "web_search". Errors are returned as
// errors, never panics, and every request is bounded by a 10 second timeout
// and the caller's context.
package websearch

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
)

const (
	// ToolName is the name the tool registers under.
	ToolName = "web_search"

	// MaxResults is the hard cap on results returned to the model, regardless
	// of Config.MaxResults or what the endpoint sends.
	MaxResults = 5

	// DefaultMaxResults is used when Config.MaxResults is not positive.
	DefaultMaxResults = MaxResults

	// defaultTimeout bounds one search, even when the caller's context has no
	// deadline.
	defaultTimeout = 10 * time.Second

	// maxResponseBytes bounds how much of a response body is read.
	maxResponseBytes = 1 << 20

	// maxErrorSnippetBytes bounds the response body echoed in an HTTP error.
	maxErrorSnippetBytes = 512
)

// Config configures a Searcher.
type Config struct {
	// Endpoint is the absolute http/https URL that accepts the search request
	// described in the package documentation. Required.
	Endpoint string

	// APIKey, when non-empty, is sent as "Authorization: Bearer <APIKey>".
	APIKey string

	// MaxResults caps results per search. Values <= 0 use DefaultMaxResults;
	// values above MaxResults are clamped to MaxResults.
	MaxResults int

	// HTTPClient overrides the HTTP client. Nil uses a client with a 10 second
	// timeout. A supplied client is still called with a 10 second request
	// context.
	HTTPClient *http.Client
}

// Result is one search hit.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Searcher performs searches against a generic HTTP endpoint.
//
// A Searcher is safe for concurrent use.
type Searcher struct {
	endpoint   string
	apiKey     string
	maxResults int
	client     *http.Client
}

// NewSearcher validates cfg and returns a Searcher.
func NewSearcher(cfg Config) (*Searcher, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("websearch: endpoint is required")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("websearch: invalid endpoint %q: %w", cfg.Endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("websearch: endpoint %q must use http or https", cfg.Endpoint)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("websearch: endpoint %q has no host", cfg.Endpoint)
	}

	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	if maxResults > MaxResults {
		maxResults = MaxResults
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Searcher{
		endpoint:   endpoint,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		maxResults: maxResults,
		client:     client,
	}, nil
}

// MaxResults reports the configured per-search result cap.
func (s *Searcher) MaxResults() int { return s.maxResults }

// Search queries the endpoint and returns at most count results. A count <= 0
// means the configured default. Results are always capped at MaxResults.
func (s *Searcher) Search(ctx context.Context, query string, count int) ([]Result, error) {
	if s == nil {
		return nil, fmt.Errorf("websearch: nil searcher")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("websearch: query is required")
	}
	if count <= 0 {
		count = s.maxResults
	}
	if count > s.maxResults {
		count = s.maxResults
	}
	if count > MaxResults {
		count = MaxResults
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("websearch: search %q: %w", query, err)
	}

	payload, err := json.Marshal(struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}{Query: query, Count: count})
	if err != nil {
		return nil, fmt.Errorf("websearch: encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("websearch: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZeoKeel/1.0 (web_search)")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("websearch: search %q: %w", query, ctxErr)
		}
		return nil, fmt.Errorf("websearch: search %q: %w", query, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("websearch: read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("websearch: response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if detail := truncate(strings.TrimSpace(string(body)), maxErrorSnippetBytes); detail != "" {
			return nil, fmt.Errorf("websearch: endpoint returned %s: %s", resp.Status, detail)
		}
		return nil, fmt.Errorf("websearch: endpoint returned %s", resp.Status)
	}

	var decoded struct {
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("websearch: decode response: %w", err)
	}
	results := decoded.Results
	if len(results) > count {
		results = results[:count]
	}
	return results, nil
}

// Tool exposes a Searcher as the "web_search" runtime.Tool.
type Tool struct {
	searcher *Searcher
}

// New validates cfg and returns the web_search tool.
func New(cfg Config) (*Tool, error) {
	s, err := NewSearcher(cfg)
	if err != nil {
		return nil, err
	}
	return &Tool{searcher: s}, nil
}

// Name implements runtime.Tool.
func (t *Tool) Name() string { return ToolName }

const toolDescription = `Search the web and return ranked results.

The search backend is configured by the operator. Results are returned as one block per hit, "[n] title", the URL, and a snippet, capped at 5 hits. The query is required.

Args JSON schema:
{"type":"object","properties":{"query":{"type":"string","description":"Search query."}},"required":["query"],"additionalProperties":false}`

// Description implements runtime.Tool.
func (t *Tool) Description() string { return toolDescription }

// Invoke implements runtime.Tool. It accepts {"query":"..."} and formats up to
// five results as "[1] title\nurl\nsnippet" blocks. Failures are returned as
// errors so the agent can report them to the model.
func (t *Tool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil || t.searcher == nil {
		return "", fmt.Errorf("websearch: tool is not initialized")
	}
	var a struct {
		Query string `json:"query"`
	}
	if len(bytes.TrimSpace(args)) > 0 && !bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			return "", fmt.Errorf("%s: invalid arguments: %w", ToolName, err)
		}
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return "", fmt.Errorf("%s: query is required", ToolName)
	}

	results, err := t.searcher.Search(ctx, query, 0)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No results found.", nil
	}

	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteByte('\n')
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "[%d] %s\n%s", i+1, title, strings.TrimSpace(r.URL))
		if snippet := strings.TrimSpace(r.Snippet); snippet != "" {
			b.WriteByte('\n')
			b.WriteString(snippet)
		}
	}
	return b.String(), nil
}

// truncate shortens s to at most n bytes without splitting a UTF-8 rune. It is
// only used for untrusted error detail, so it does not add a marker.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}
