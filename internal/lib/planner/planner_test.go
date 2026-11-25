package planner

// These tests are the specification for the wire half of the Lead's triage
// pass. They never touch a network: every case is an httptest server that
// either answers with a canned OpenAI-shaped body or records what the planner
// sent. The last test is the security one — no failure path may ever echo the
// API key back into an error message, even when the endpoint itself echoes the
// Authorization header.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// testAPIKey is a recognizable value so a leak assertion can look for it.
	testAPIKey = "sk-test-do-not-leak-0123456789abcdef"
	testModel  = "triage-test-model"
	// testSystemPrompt is asserted literally: the system line is part of the
	// contract with the model, not an implementation detail.
	testSystemPrompt = "you are a triage planner that replies with a single JSON object"
)

// countingServer starts an httptest server around handler and counts every
// request it receives. The count is atomic because the handler runs on the
// server's goroutine.
func countingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

// answeringServer starts a server that answers every request with status and body.
func answeringServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	return countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// newPlanner builds a planner pointed at baseURL. tune may adjust the config
// before construction (budgets, timeouts, credentials).
func newPlanner(t *testing.T, baseURL string, tune ...func(*Config)) *Planner {
	t.Helper()
	cfg := Config{BaseURL: baseURL, APIKey: testAPIKey, Model: testModel}
	for _, adjust := range tune {
		adjust(&cfg)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%q) returned unexpected error: %v", baseURL, err)
	}
	return p
}

// chatBody builds an OpenAI-shaped answer carrying content in message.content.
func chatBody(content string) string {
	payload, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
	})
	if err != nil {
		panic(err) // a fixed shape cannot fail to encode
	}
	return string(payload)
}

// TestNewValidatesConfig covers the constructor: required fields, the endpoint
// normalization, and every default.
func TestNewValidatesConfig(t *testing.T) {
	t.Run("empty base URL is rejected", func(t *testing.T) {
		for _, baseURL := range []string{"", "   "} {
			if _, err := New(Config{BaseURL: baseURL, Model: testModel}); !errors.Is(err, ErrPlannerInvalidConfig) {
				t.Fatalf("New(BaseURL=%q) error = %v, want ErrPlannerInvalidConfig", baseURL, err)
			}
		}
	})

	t.Run("empty model is rejected", func(t *testing.T) {
		if _, err := New(Config{BaseURL: "https://example.invalid/v1", Model: "  "}); !errors.Is(err, ErrPlannerInvalidConfig) {
			t.Fatalf("error = %v, want ErrPlannerInvalidConfig", err)
		}
	})

	t.Run("unusable endpoint is rejected", func(t *testing.T) {
		for _, baseURL := range []string{"example.invalid/v1", "ftp://example.invalid/v1", "://bad", "http://bad host/v1", "https:///v1"} {
			if _, err := New(Config{BaseURL: baseURL, Model: testModel}); !errors.Is(err, ErrPlannerInvalidConfig) {
				t.Fatalf("New(BaseURL=%q) error = %v, want ErrPlannerInvalidConfig", baseURL, err)
			}
		}
	})

	t.Run("defaults are applied", func(t *testing.T) {
		p := newPlanner(t, "https://example.invalid/v1/")
		if p.endpoint != "https://example.invalid/v1" {
			t.Errorf("endpoint = %q, want the trailing slash trimmed", p.endpoint)
		}
		if p.timeout != DefaultTimeout {
			t.Errorf("timeout = %v, want default %v", p.timeout, DefaultTimeout)
		}
		if p.maxTokens != DefaultMaxTokens {
			t.Errorf("maxTokens = %d, want default %d", p.maxTokens, DefaultMaxTokens)
		}
		if p.maxPromptRunes != DefaultMaxPromptRunes {
			t.Errorf("maxPromptRunes = %d, want default %d", p.maxPromptRunes, DefaultMaxPromptRunes)
		}
		if p.client == nil {
			t.Fatal("client is nil, want a default client")
		}
	})

	t.Run("explicit values win", func(t *testing.T) {
		client := &http.Client{}
		p := newPlanner(t, "https://example.invalid/v1", func(cfg *Config) {
			cfg.Timeout = 3 * time.Second
			cfg.MaxTokens = 77
			cfg.MaxPromptRunes = 99
			cfg.Client = client
		})
		if p.timeout != 3*time.Second {
			t.Errorf("timeout = %v, want 3s", p.timeout)
		}
		if p.maxTokens != 77 {
			t.Errorf("maxTokens = %d, want 77", p.maxTokens)
		}
		if p.maxPromptRunes != 99 {
			t.Errorf("maxPromptRunes = %d, want 99", p.maxPromptRunes)
		}
		if p.client != client {
			t.Error("client was replaced; an injected client must be used as given")
		}
	})

	t.Run("planner satisfies the contract", func(t *testing.T) {
		var model contracts.PlannerModel = newPlanner(t, "https://example.invalid/v1")
		if model == nil {
			t.Fatal("the planner must satisfy contracts.PlannerModel")
		}
	})
}

// TestDecideHappyPath asserts the whole wire request: method, path (with the
// base URL's trailing slash trimmed), auth, content type, model, temperature,
// max_tokens, and both messages.
func TestDecideHappyPath(t *testing.T) {
	const prompt = "ITEM\n<<<DATA\nbody: use {these} braces\nDATA>>>\nDECIDE"
	const answer = `{"action":"skip","note":"unclear"}`

	var checked atomic.Bool
	srv, count := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q, want the bearer key", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("request body is not an object: %v (%s)", err, raw)
		}
		for _, key := range []string{"model", "messages", "temperature", "max_tokens"} {
			if _, ok := fields[key]; !ok {
				t.Errorf("request body is missing %q: %s", key, raw)
			}
		}
		if got := string(fields["temperature"]); got != "0" {
			t.Errorf("temperature = %s, want 0 (deterministic triage)", got)
		}
		if got := string(fields["max_tokens"]); got != "512" {
			t.Errorf("max_tokens = %s, want the default 512", got)
		}
		if got := string(fields["model"]); got != `"`+testModel+`"` {
			t.Errorf("model = %s, want %q", got, testModel)
		}

		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode messages: %v", err)
		}
		if len(body.Messages) != 2 {
			t.Fatalf("messages = %d, want 2 (system then user)", len(body.Messages))
		}
		if body.Messages[0].Role != "system" || body.Messages[0].Content != testSystemPrompt {
			t.Errorf("messages[0] = %+v, want the fixed system line", body.Messages[0])
		}
		if body.Messages[1].Role != "user" || body.Messages[1].Content != prompt {
			t.Errorf("messages[1] = %+v, want the prompt verbatim", body.Messages[1])
		}
		checked.Store(true)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatBody(answer))
	})

	p := newPlanner(t, srv.URL+"/v1/")
	got, err := p.Decide(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if got != answer {
		t.Errorf("Decide = %q, want %q", got, answer)
	}
	if !checked.Load() {
		t.Fatal("the endpoint handler never ran its assertions")
	}
	if n := count.Load(); n != 1 {
		t.Errorf("requests = %d, want exactly 1", n)
	}
}

// TestDecideLegacyTextShape covers endpoints that answer with choices[].text
// instead of choices[].message.content.
func TestDecideLegacyTextShape(t *testing.T) {
	srv, _ := answeringServer(t, http.StatusOK, `{"choices":[{"text":"  {\"action\":\"ignore\"}\n"}]}`)
	got, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if want := `{"action":"ignore"}`; got != want {
		t.Errorf("Decide = %q, want %q", got, want)
	}
}

// TestDecidePrefersMessageContent locks in the precedence when an endpoint
// sends both shapes.
func TestDecidePrefersMessageContent(t *testing.T) {
	body := `{"choices":[{"message":{"content":"{\"action\":\"task\"}"},"text":"{\"action\":\"ignore\"}"}]}`
	srv, _ := answeringServer(t, http.StatusOK, body)
	got, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if want := `{"action":"task"}`; got != want {
		t.Errorf("Decide = %q, want %q (message.content must win)", got, want)
	}
}

// TestDecideTypedStatusErrors maps the endpoint's refusals onto the typed errors
// the caller branches on.
func TestDecideTypedStatusErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		want      error
		notWanted error
	}{
		{name: "401 wants a new key", status: http.StatusUnauthorized, body: `{"error":{"message":"invalid api key"}}`,
			want: ErrPlannerUnauthorized, notWanted: ErrPlannerUnavailable},
		{name: "403 forbidden", status: http.StatusForbidden, body: `{"error":{"message":"forbidden"}}`,
			want: ErrPlannerUnauthorized, notWanted: ErrPlannerRateLimited},
		{name: "429 means back off", status: http.StatusTooManyRequests, body: `{"error":{"message":"slow down"}}`,
			want: ErrPlannerRateLimited, notWanted: ErrPlannerUnauthorized},
		{name: "500 is transient", status: http.StatusInternalServerError, body: `not json at all`,
			want: ErrPlannerUnavailable, notWanted: ErrPlannerUnauthorized},
		{name: "503 is transient", status: http.StatusServiceUnavailable, body: ``,
			want: ErrPlannerUnavailable, notWanted: ErrPlannerRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, count := answeringServer(t, tc.status, tc.body)
			answer, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			if err == nil {
				t.Fatalf("Decide returned nil error for status %d (answer %q)", tc.status, answer)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want errors.Is(_, %v)", err, tc.want)
			}
			if errors.Is(err, tc.notWanted) {
				t.Errorf("error = %v, must not match %v", err, tc.notWanted)
			}
			if answer != "" {
				t.Errorf("answer = %q, want empty on failure", answer)
			}
			if n := count.Load(); n != 1 {
				t.Errorf("requests = %d, want exactly 1", n)
			}
		})
	}
}

// TestDecideRateLimitMentionsRetryAfter keeps the provider's hint: the loop can
// only back off sensibly if it knows how long.
func TestDecideRateLimitMentionsRetryAfter(t *testing.T) {
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"slow down"}`)
	})
	_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
	if !errors.Is(err, ErrPlannerRateLimited) {
		t.Fatalf("error = %v, want ErrPlannerRateLimited", err)
	}
	if !strings.Contains(err.Error(), "7") {
		t.Errorf("error = %q, want the Retry-After hint", err)
	}
}

// TestDecideEmptyAnswers covers every shape that carries no usable text.
func TestDecideEmptyAnswers(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "no choices", body: `{"choices":[]}`},
		{name: "no choices key", body: `{}`},
		{name: "blank content", body: `{"choices":[{"message":{"content":"   \n "}}]}`},
		{name: "blank legacy text", body: `{"choices":[{"text":"\t"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := answeringServer(t, http.StatusOK, tc.body)
			answer, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			if !errors.Is(err, ErrPlannerEmpty) {
				t.Fatalf("error = %v, want ErrPlannerEmpty", err)
			}
			if answer != "" {
				t.Errorf("answer = %q, want empty", answer)
			}
		})
	}
}

// TestDecideUnparsableAnswerIsUnavailable treats a 2xx body that is not JSON as
// a broken endpoint, not as a model answer.
func TestDecideUnparsableAnswerIsUnavailable(t *testing.T) {
	srv, _ := answeringServer(t, http.StatusOK, `<html>gateway timeout</html>`)
	_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
	if !errors.Is(err, ErrPlannerUnavailable) {
		t.Fatalf("error = %v, want ErrPlannerUnavailable", err)
	}
}

// TestDecideNetworkFailure covers a dead endpoint: the connection is refused, so
// the call must be reported as transient.
func TestDecideNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	_, err := newPlanner(t, deadURL+"/v1").Decide(context.Background(), "prompt")
	if !errors.Is(err, ErrPlannerUnavailable) {
		t.Fatalf("error = %v, want ErrPlannerUnavailable", err)
	}
}

// TestDecidePromptBudget proves the budget is enforced before the call: an
// oversized prompt must not cost a network round trip, and the exact limit must
// still be accepted.
func TestDecidePromptBudget(t *testing.T) {
	t.Run("default limit is respected", func(t *testing.T) {
		srv, count := answeringServer(t, http.StatusOK, chatBody(`{"action":"skip"}`))
		p := newPlanner(t, srv.URL+"/v1")

		if _, err := p.Decide(context.Background(), strings.Repeat("x", DefaultMaxPromptRunes+1)); !errors.Is(err, ErrPlannerPromptTooLong) {
			t.Fatalf("error = %v, want ErrPlannerPromptTooLong", err)
		}
		if n := count.Load(); n != 0 {
			t.Fatalf("requests = %d, want 0 (the budget must be checked before the call)", n)
		}

		if _, err := p.Decide(context.Background(), strings.Repeat("x", DefaultMaxPromptRunes)); err != nil {
			t.Fatalf("a prompt at the limit was rejected: %v", err)
		}
		if n := count.Load(); n != 1 {
			t.Fatalf("requests = %d, want 1", n)
		}
	})

	t.Run("custom limit counts runes, not bytes", func(t *testing.T) {
		srv, count := answeringServer(t, http.StatusOK, chatBody(`{"action":"skip"}`))
		p := newPlanner(t, srv.URL+"/v1", func(cfg *Config) { cfg.MaxPromptRunes = 4 })

		// Four multi-byte runes are 12 bytes but only 4 characters: accepted.
		if _, err := p.Decide(context.Background(), "项目计划"); err != nil {
			t.Fatalf("4 runes (12 bytes) against a 4-rune budget was rejected: %v", err)
		}
		if _, err := p.Decide(context.Background(), "项目计划书"); !errors.Is(err, ErrPlannerPromptTooLong) {
			t.Fatalf("error = %v, want ErrPlannerPromptTooLong", err)
		}
		if n := count.Load(); n != 1 {
			t.Fatalf("requests = %d, want 1 (only the accepted prompt was sent)", n)
		}
	})
}

// TestDecideHonorsContext covers both cancellation before the call and
// cancellation while the endpoint is still thinking.
func TestDecideHonorsContext(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		srv, count := answeringServer(t, http.StatusOK, chatBody(`{"action":"skip"}`))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := newPlanner(t, srv.URL+"/v1").Decide(ctx, "prompt")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if n := count.Load(); n != 0 {
			t.Fatalf("requests = %d, want 0", n)
		}
	})

	t.Run("cancelled while waiting", func(t *testing.T) {
		// The handler parks until the test releases it: the point is that Decide
		// stops waiting on its own, not that the server notices the disconnect.
		started := make(chan struct{})
		release := make(chan struct{})
		srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			// Return as soon as the client gives up: a handler that only waited
			// for the test would make httptest.Server.Close block forever under
			// load (seen once in a full-suite run, audit F9).
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		t.Cleanup(func() { close(release) })

		p := newPlanner(t, srv.URL+"/v1")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			_, err := p.Decide(ctx, "prompt")
			done <- err
		}()

		<-started
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			if !errors.Is(err, ErrPlannerUnavailable) {
				t.Errorf("error = %v, want it classified as ErrPlannerUnavailable too", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Decide ignored context cancellation")
		}
	})
}

// TestDecideWithoutAPIKey covers a keyless local endpoint: the Authorization
// header must simply be absent, not "Bearer " with nothing after it.
func TestDecideWithoutAPIKey(t *testing.T) {
	auth := make(chan string, 1)
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatBody(`{"action":"skip"}`))
	})

	p := newPlanner(t, srv.URL+"/v1", func(cfg *Config) { cfg.APIKey = "" })
	answer, err := p.Decide(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if answer != `{"action":"skip"}` {
		t.Errorf("answer = %q, want the endpoint's answer", answer)
	}
	if got := <-auth; got != "" {
		t.Errorf("Authorization = %q, want no header when no key is configured", got)
	}

	// A keyless endpoint can still refuse: the redaction helper must not treat
	// the empty key as a pattern to splice between every character.
	bad, _ := answeringServer(t, http.StatusInternalServerError, "upstream exploded")
	p = newPlanner(t, bad.URL+"/v1", func(cfg *Config) { cfg.APIKey = "" })
	_, err = p.Decide(context.Background(), "prompt")
	if !errors.Is(err, ErrPlannerUnavailable) {
		t.Fatalf("error = %v, want ErrPlannerUnavailable", err)
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want the endpoint's explanation kept verbatim", err)
	}
}

// TestStatusErrorSnippetIsBounded keeps an endpoint's error page out of our
// logs: the copied text is one short truncated line.
func TestStatusErrorSnippetIsBounded(t *testing.T) {
	srv, _ := answeringServer(t, http.StatusInternalServerError, strings.Repeat("boom ", 500))
	_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
	if !errors.Is(err, ErrPlannerUnavailable) {
		t.Fatalf("error = %v, want ErrPlannerUnavailable", err)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Errorf("error = %q, want a truncation marker", err)
	}
	if runes := []rune(err.Error()); len(runes) > maxErrorSnippetRunes+100 {
		t.Errorf("error text is %d runes, want it bounded near %d", len(runes), maxErrorSnippetRunes)
	}
}

// TestErrorsNeverLeakTheAPIKey is the security contract: an endpoint (or a
// misconfigured URL) must never be able to push the credential into a log, and
// even an endpoint that echoes the Authorization header must be redacted.
func TestErrorsNeverLeakTheAPIKey(t *testing.T) {
	echoing := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"bad credential: `+r.Header.Get("Authorization")+`"}`)
	}
	cases := []struct {
		name string
		// wantRedacted marks the cases where the endpoint echoes the credential:
		// they must not only avoid the key, they must show it was scrubbed.
		wantRedacted bool
		// err builds the error under test. Each case may stand up its own server.
		err func(t *testing.T) error
	}{
		{name: "401 echoing the header", wantRedacted: true, err: func(t *testing.T) error {
			srv, _ := countingServer(t, echoing)
			_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "403 body echoing the header", wantRedacted: true, err: func(t *testing.T) error {
			srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, "denied for "+testAPIKey)
			})
			_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "429 body echoing the key", wantRedacted: true, err: func(t *testing.T) error {
			srv, _ := answeringServer(t, http.StatusTooManyRequests, "quota "+testAPIKey)
			_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "500 body echoing the key", wantRedacted: true, err: func(t *testing.T) error {
			srv, _ := answeringServer(t, http.StatusInternalServerError, "panic near "+testAPIKey)
			_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "unparsable 2xx body", err: func(t *testing.T) error {
			srv, _ := answeringServer(t, http.StatusOK, "<html>"+testAPIKey+"</html>")
			_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "empty answer", err: func(t *testing.T) error {
			srv, _ := answeringServer(t, http.StatusOK, `{"choices":[]}`)
			_, err := newPlanner(t, srv.URL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "network failure", err: func(t *testing.T) error {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			deadURL := srv.URL
			srv.Close()
			_, err := newPlanner(t, deadURL+"/v1").Decide(context.Background(), "prompt")
			return err
		}},
		{name: "prompt too long", err: func(t *testing.T) error {
			srv, _ := answeringServer(t, http.StatusOK, chatBody(`{"action":"skip"}`))
			_, err := newPlanner(t, srv.URL+"/v1", func(cfg *Config) { cfg.MaxPromptRunes = 2 }).
				Decide(context.Background(), "much too long")
			return err
		}},
		{name: "cancelled context", err: func(t *testing.T) error {
			srv, _ := answeringServer(t, http.StatusOK, chatBody(`{"action":"skip"}`))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := newPlanner(t, srv.URL+"/v1").Decide(ctx, "prompt")
			return err
		}},
		{name: "invalid config", err: func(t *testing.T) error {
			_, err := New(Config{BaseURL: "", Model: "", APIKey: testAPIKey})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.err(t)
			if err == nil {
				t.Fatal("expected an error to inspect")
			}
			text := err.Error()
			if strings.Contains(text, testAPIKey) {
				t.Fatalf("error leaks the API key: %q", text)
			}
			if strings.Contains(text, "Bearer "+testAPIKey) {
				t.Fatalf("error leaks the bearer credential: %q", text)
			}
			if tc.wantRedacted && !strings.Contains(text, "[redacted]") {
				t.Fatalf("error = %q, want the echoed credential replaced with [redacted]", text)
			}
		})
	}
}
