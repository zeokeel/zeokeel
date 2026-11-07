package websearch

// Tests for the generic HTTP search client: request shape, auth header, result
// parsing/limits, validation, errors, cancellation/timeouts and concurrency.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// searchServer answers one search request with the configured handler and
// records the request JSON/headers.
type searchServer struct {
	*httptest.Server
	mu   sync.Mutex
	last *searchRequest
}

type searchRequest struct {
	method        string
	contentType   string
	accept        string
	userAgent     string
	authorization string
	query         string
	count         int
}

func newSearchServer(t *testing.T, status int, body string) *searchServer {
	t.Helper()
	s := &searchServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		_ = json.Unmarshal(raw, &payload)
		s.mu.Lock()
		s.last = &searchRequest{
			method:        r.Method,
			contentType:   r.Header.Get("Content-Type"),
			accept:        r.Header.Get("Accept"),
			userAgent:     r.Header.Get("User-Agent"),
			authorization: r.Header.Get("Authorization"),
			query:         payload.Query,
			count:         payload.Count,
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *searchServer) request() searchRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return searchRequest{}
	}
	return *s.last
}

func resultsBody(titles ...string) string {
	entries := make([]string, 0, len(titles))
	for index, title := range titles {
		entries = append(entries, fmt.Sprintf(`{"title":%q,"url":"https://example.com/%d","snippet":"snippet %d"}`,
			title, index, index))
	}
	return `{"results":[` + strings.Join(entries, ",") + `]}`
}

func TestNewSearcherValidation(t *testing.T) {
	valid := []string{
		"http://example.com/search",
		"https://example.com/search?tenant=1",
		"http://127.0.0.1:8080/search",
	}
	for _, endpoint := range valid {
		if _, err := NewSearcher(Config{Endpoint: endpoint}); err != nil {
			t.Fatalf("NewSearcher(%q) = %v", endpoint, err)
		}
	}
	invalid := map[string]string{
		"empty":      "",
		"blank":      "   ",
		"ftp scheme": "ftp://example.com/search",
		"no scheme":  "example.com/search",
		"no host":    "https:///search",
		"bad url":    "http://[::1",
		"javascript": "javascript:alert(1)",
	}
	for name, endpoint := range invalid {
		if _, err := NewSearcher(Config{Endpoint: endpoint}); err == nil {
			t.Fatalf("NewSearcher(%s: %q) returned nil error", name, endpoint)
		}
	}
}

func TestNewSearcherClampsMaxResults(t *testing.T) {
	cases := map[int]int{0: DefaultMaxResults, -3: DefaultMaxResults, 2: 2, MaxResults: MaxResults, 99: MaxResults}
	for configured, want := range cases {
		searcher, err := NewSearcher(Config{Endpoint: "https://example.com", MaxResults: configured})
		if err != nil {
			t.Fatalf("NewSearcher(max=%d): %v", configured, err)
		}
		if got := searcher.MaxResults(); got != want {
			t.Fatalf("MaxResults(max=%d) = %d, want %d", configured, got, want)
		}
	}
}

func TestSearchRequestShapeAndResults(t *testing.T) {
	server := newSearchServer(t, http.StatusOK, resultsBody("one", "two"))
	searcher, err := NewSearcher(Config{Endpoint: server.URL, APIKey: " secret-key ", MaxResults: 3})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	results, err := searcher.Search(context.Background(), "  golang modules  ", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 || results[0].Title != "one" || results[1].URL != "https://example.com/1" ||
		results[0].Snippet != "snippet 0" {
		t.Fatalf("results = %+v", results)
	}
	request := server.request()
	if request.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", request.method)
	}
	if request.contentType != "application/json" || request.accept != "application/json" {
		t.Fatalf("headers = content-type %q accept %q", request.contentType, request.accept)
	}
	if request.userAgent == "" {
		t.Fatal("request has no User-Agent")
	}
	if request.authorization != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", request.authorization)
	}
	if request.query != "golang modules" || request.count != 2 {
		t.Fatalf("payload = %+v, want trimmed query and count 2", request)
	}
}

func TestSearchWithoutAPIKeyOmitsAuthorization(t *testing.T) {
	server := newSearchServer(t, http.StatusOK, resultsBody("one"))
	searcher, err := NewSearcher(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if _, err := searcher.Search(context.Background(), "q", 0); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := server.request().authorization; got != "" {
		t.Fatalf("Authorization = %q, want omitted", got)
	}
}

func TestSearchResultLimit(t *testing.T) {
	server := newSearchServer(t, http.StatusOK, resultsBody("1", "2", "3", "4", "5", "6", "7"))
	searcher, err := NewSearcher(Config{Endpoint: server.URL, MaxResults: 3})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	results, err := searcher.Search(context.Background(), "q", 99)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want the configured cap 3", len(results))
	}
	if got := server.request().count; got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}

	// count <= 0 uses the configured default (also clamped to MaxResults).
	results, err = searcher.Search(context.Background(), "q", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 || server.request().count != 3 {
		t.Fatalf("default count results = %d request = %d, want 3/3", len(results), server.request().count)
	}

	// The package-wide MaxResults cap wins even when the endpoint returns more.
	capped, err := NewSearcher(Config{Endpoint: server.URL, MaxResults: 99})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	results, err = capped.Search(context.Background(), "q", 99)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != MaxResults || server.request().count != MaxResults {
		t.Fatalf("package cap = %d/%d, want %d", len(results), server.request().count, MaxResults)
	}
}

func TestSearchValidation(t *testing.T) {
	searcher, err := NewSearcher(Config{Endpoint: "https://example.com"})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if _, err := searcher.Search(context.Background(), "   ", 0); err == nil {
		t.Fatal("Search(empty query) returned nil error")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := searcher.Search(cancelled, "q", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search(cancelled ctx) = %v, want context.Canceled", err)
	}
	// A nil context means background: it must go through the normal request
	// path (no panic) and return the parsed results.
	nilCtxServer := newSearchServer(t, http.StatusOK, resultsBody("nil-context"))
	nilCtxSearcher, err := NewSearcher(Config{Endpoint: nilCtxServer.URL})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	results, err := nilCtxSearcher.Search(nil, "q", 0)
	if err != nil || len(results) != 1 || results[0].Title != "nil-context" {
		t.Fatalf("Search(nil ctx) = %+v, %v", results, err)
	}
}

func TestSearchHTTPErrors(t *testing.T) {
	server := newSearchServer(t, http.StatusUnauthorized, `{"error":"invalid api key"}`)
	searcher, err := NewSearcher(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	_, err = searcher.Search(context.Background(), "q", 0)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("Search(non-2xx) = %v, want status and body detail", err)
	}

	badJSON := newSearchServer(t, http.StatusOK, "not-json")
	badSearcher, err := NewSearcher(Config{Endpoint: badJSON.URL})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if _, err := badSearcher.Search(context.Background(), "q", 0); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("Search(bad JSON) = %v, want decode error", err)
	}
}

func TestSearchResponseLimit(t *testing.T) {
	server := newSearchServer(t, http.StatusOK, strings.Repeat("x", maxResponseBytes+10))
	searcher, err := NewSearcher(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if _, err := searcher.Search(context.Background(), "q", 0); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Search(oversized body) = %v, want response-limit error", err)
	}
}

func TestSearchTimeoutAndCustomClient(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(block)
	client := &http.Client{Timeout: 30 * time.Millisecond}
	searcher, err := NewSearcher(Config{Endpoint: server.URL, HTTPClient: client})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	start := time.Now()
	_, err = searcher.Search(context.Background(), "q", 0)
	if err == nil {
		t.Fatal("Search against a stalled server returned nil error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Search took %v, want the client timeout to bound it", elapsed)
	}
}

func TestSearchConcurrent(t *testing.T) {
	server := newSearchServer(t, http.StatusOK, resultsBody("one", "two"))
	searcher, err := NewSearcher(Config{Endpoint: server.URL, MaxResults: 2})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	var wg sync.WaitGroup
	var failures atomic.Int32
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				results, err := searcher.Search(context.Background(), "q", 2)
				if err != nil || len(results) != 2 {
					failures.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent searches failed %d times", failures.Load())
	}
}
