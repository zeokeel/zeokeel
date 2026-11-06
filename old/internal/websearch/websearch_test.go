package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSearchSuccessFormatting(t *testing.T) {
	var (
		mu       sync.Mutex
		gotQuery string
		gotCount int
		gotAuth  string
		gotCT    string
		gotMeth  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMeth, gotAuth, gotCT = r.Method, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		var body struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery, gotCount = body.Query, body.Count
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"First result","url":"https://example.com/a","snippet":"A snippet"},
			{"title":"Second result","url":"https://example.com/b","snippet":"Another snippet"}
		]}`))
	}))
	defer srv.Close()

	tool, err := New(Config{Endpoint: srv.URL, APIKey: "secret", MaxResults: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tool.Name() != "web_search" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), `"query"`) {
		t.Fatalf("Description() lacks query schema: %q", tool.Description())
	}

	out, err := invoke(t, tool, map[string]any{"query": "  golang testing  "})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	want := "[1] First result\nhttps://example.com/a\nA snippet\n[2] Second result\nhttps://example.com/b\nAnother snippet"
	if out != want {
		t.Fatalf("Invoke output =\n%q\nwant\n%q", out, want)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotMeth != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMeth)
	}
	if gotQuery != "golang testing" {
		t.Errorf("query = %q, want trimmed query", gotQuery)
	}
	if gotCount != 3 {
		t.Errorf("count = %d, want 3", gotCount)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Errorf("Content-Type = %q", gotCT)
	}
}

func TestSearchCountCaps(t *testing.T) {
	var mu sync.Mutex
	var gotCount int
	results := make([]string, 0, 8)
	for i := 1; i <= 8; i++ {
		results = append(results, `{"title":"T`+string(rune('0'+i))+`","url":"https://example.com/`+string(rune('0'+i))+`","snippet":"s"}`)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Count int `json:"count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		gotCount = body.Count
		mu.Unlock()
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(results, ",") + `]}`))
	}))
	defer srv.Close()

	// The hard cap applies even when the operator asks for more.
	tool, err := New(Config{Endpoint: srv.URL, MaxResults: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := invoke(t, tool, map[string]any{"query": "many"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	mu.Lock()
	if gotCount != MaxResults {
		t.Errorf("request count = %d, want hard cap %d", gotCount, MaxResults)
	}
	mu.Unlock()
	if !strings.Contains(out, "[5]") || strings.Contains(out, "[6]") {
		t.Fatalf("output was not capped at 5 results:\n%s", out)
	}

	// A smaller configured cap limits both the request and the output.
	small, err := New(Config{Endpoint: srv.URL, MaxResults: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err = invoke(t, small, map[string]any{"query": "two"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	mu.Lock()
	if gotCount != 2 {
		t.Errorf("request count = %d, want 2", gotCount)
	}
	mu.Unlock()
	if !strings.Contains(out, "[2]") || strings.Contains(out, "[3]") {
		t.Fatalf("output was not capped at 2 results:\n%s", out)
	}
}

func TestSearchNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	tool, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := invoke(t, tool, map[string]any{"query": "nothing"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out != "No results found." {
		t.Fatalf("empty result output = %q", out)
	}
}

func TestSearchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("search backend is down"))
	}))
	defer srv.Close()

	tool, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = invoke(t, tool, map[string]any{"query": "fail"})
	if err == nil {
		t.Fatal("Invoke succeeded on a 503")
	}
	for _, want := range []string{"503", "search backend is down"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestSearchBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[`))
	}))
	defer srv.Close()

	tool, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = invoke(t, tool, map[string]any{"query": "bad"})
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("bad JSON error = %v", err)
	}
}

func TestSearchTimeoutAndCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The client cancels the request long before this sleep ends. A
		// sleep (rather than waiting on r.Context()) keeps httptest.Server
		// Close from blocking forever: the server does not observe a client
		// disconnect until it writes.
		time.Sleep(250 * time.Millisecond)
	}))
	defer srv.Close()

	tool, err := New(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = tool.Invoke(ctx, mustJSON(t, map[string]any{"query": "slow"}))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s, should honor ctx promptly", elapsed)
	}

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	_, err = tool.Invoke(canceled, mustJSON(t, map[string]any{"query": "canceled"}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v, want context.Canceled", err)
	}
}

func TestSearchDefaultDeadline(t *testing.T) {
	var (
		hasDeadline bool
		remaining   time.Duration
	)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		hasDeadline = ok
		if ok {
			remaining = time.Until(deadline)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"results":[]}`)),
			Request:    r,
		}, nil
	})}
	tool, err := New(Config{Endpoint: "https://example.com/search", HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tool.Invoke(context.Background(), mustJSON(t, map[string]any{"query": "deadline"})); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !hasDeadline {
		t.Fatal("request context has no deadline")
	}
	if remaining <= 0 || remaining > 11*time.Second {
		t.Fatalf("deadline is %s away, want about 10s", remaining)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewValidation(t *testing.T) {
	for _, endpoint := range []string{"", "   ", "ftp://example.com/search", "http:///nohost", "://bad"} {
		if _, err := New(Config{Endpoint: endpoint}); err == nil {
			t.Errorf("New accepted endpoint %q", endpoint)
		}
	}
	if _, err := New(Config{}); err == nil {
		t.Error("New accepted an empty config")
	}

	s, err := NewSearcher(Config{Endpoint: "https://example.com/search", MaxResults: 100})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if s.MaxResults() != MaxResults {
		t.Fatalf("MaxResults() = %d, want %d", s.MaxResults(), MaxResults)
	}
	s, err = NewSearcher(Config{Endpoint: "https://example.com/search"})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if s.MaxResults() != DefaultMaxResults {
		t.Fatalf("default MaxResults() = %d, want %d", s.MaxResults(), DefaultMaxResults)
	}
}

func TestInvokeArgumentValidation(t *testing.T) {
	tool, err := New(Config{Endpoint: "https://example.com/search"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := tool.Invoke(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("nil args error = %v", err)
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON args accepted")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"query":"x","extra":1}`)); err == nil {
		t.Fatal("unknown argument accepted")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"query":"   "}`)); err == nil {
		t.Fatal("blank query accepted")
	}
	if _, err := (*Tool)(nil).Invoke(context.Background(), nil); err == nil {
		t.Fatal("nil tool Invoke did not fail")
	}
}

func invoke(t *testing.T, tool *Tool, args any) (string, error) {
	t.Helper()
	return tool.Invoke(context.Background(), mustJSON(t, args))
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}
