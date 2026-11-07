package sessionstore

// Tests for cross-session message search: matching, ordering, snippets, LIKE
// escaping, limits, deletion/reopen and concurrency.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// appendText appends plain text messages of the given roles to a session.
func appendText(t *testing.T, current *store, sessionID string, messages ...rig.Message) {
	t.Helper()
	if err := current.History().Append(context.Background(), sessionID, messages...); err != nil {
		t.Fatalf("append to %q: %v", sessionID, err)
	}
}

func TestSearchMessagesAcrossSessions(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "s1", "First"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	appendText(t, current, "s1",
		rig.NewMessage(rig.RoleUser, "Hello World"),
		rig.NewMessage(rig.RoleAssistant, "The GREETING is hello again"),
	)
	if err := current.CreateSession(ctx, "s2", "第二会话"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	appendText(t, current, "s2",
		rig.NewMessage(rig.RoleUser, "中文内容：你好世界"),
		rig.NewMessage(rig.RoleAssistant, "another HELLO"),
	)
	if err := current.CreateSession(ctx, "s3", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	appendText(t, current, "s3", rig.NewMessage(rig.RoleUser, "untitled hello"))

	hits, err := current.SearchMessages(ctx, "hello", 0)
	if err != nil {
		t.Fatalf("SearchMessages(hello): %v", err)
	}
	if len(hits) != 4 {
		t.Fatalf("hello hits = %d (%+v), want 4", len(hits), hits)
	}
	wantOrder := []struct {
		session string
		role    string
		text    string
	}{
		{"s3", "user", "untitled hello"},
		{"s2", "assistant", "another HELLO"},
		{"s1", "assistant", "The GREETING is hello again"},
		{"s1", "user", "Hello World"},
	}
	for index, want := range wantOrder {
		hit := hits[index]
		if hit.SessionID != want.session || hit.Role != want.role {
			t.Fatalf("hit %d = %+v, want session %q role %q", index, hit, want.session, want.role)
		}
		if !strings.Contains(strings.ToLower(hit.Snippet), "hello") {
			t.Fatalf("hit %d snippet = %q, want the match", index, hit.Snippet)
		}
		if hit.CreatedAt.IsZero() {
			t.Fatalf("hit %d has zero CreatedAt", index)
		}
	}
	if hits[1].SessionTitle != "第二会话" {
		t.Fatalf("hit session title = %q, want 第二会话", hits[1].SessionTitle)
	}
	if hits[0].SessionTitle != unnamedSessionTitle {
		t.Fatalf("empty-title session title = %q, want %q", hits[0].SessionTitle, unnamedSessionTitle)
	}

	// Chinese query and rune-safe long snippet with both ellipses.
	long := strings.Repeat("前", 100) + "关键词" + strings.Repeat("后", 100)
	appendText(t, current, "s2", rig.NewMessage(rig.RoleUser, long))
	hits, err = current.SearchMessages(ctx, "关键词", 0)
	if err != nil {
		t.Fatalf("SearchMessages(Chinese): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Chinese hits = %d, want 1", len(hits))
	}
	snippet := hits[0].Snippet
	if !utf8.ValidString(snippet) || !strings.Contains(snippet, "关键词") {
		t.Fatalf("Chinese snippet = %q", snippet)
	}
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") {
		t.Fatalf("long snippet = %q, want both ellipses", snippet)
	}
	if runes := []rune(snippet); len(runes) > 2*searchSnippetContext+3+2 {
		t.Fatalf("snippet has %d runes, want a bounded window", len(runes))
	}

	// Whitespace is collapsed in snippets.
	appendText(t, current, "s2", rig.NewMessage(rig.RoleUser, "line one\n\n line   two needle line three"))
	hits, err = current.SearchMessages(ctx, "needle", 0)
	if err != nil || len(hits) != 1 {
		t.Fatalf("SearchMessages(needle) = %d/%v", len(hits), err)
	}
	if strings.Contains(hits[0].Snippet, "\n") || strings.Contains(hits[0].Snippet, "  ") {
		t.Fatalf("snippet keeps whitespace runs: %q", hits[0].Snippet)
	}
}

func TestSearchMessagesWildcardsAreLiteral(t *testing.T) {
	current := openTestStore(t)
	appendText(t, current, "s1",
		rig.NewMessage(rig.RoleUser, "100% complete"),
		rig.NewMessage(rig.RoleUser, "under_score value"),
		rig.NewMessage(rig.RoleUser, "plain text"),
		rig.NewMessage(rig.RoleUser, `back\slash value`),
	)
	ctx := context.Background()

	percent, err := current.SearchMessages(ctx, "%", 0)
	if err != nil || len(percent) != 1 || !strings.Contains(percent[0].Snippet, "%") {
		t.Fatalf("SearchMessages(%%) = %+v/%v, want only the percent message", percent, err)
	}
	underscore, err := current.SearchMessages(ctx, "_", 0)
	if err != nil || len(underscore) != 1 || !strings.Contains(underscore[0].Snippet, "_") {
		t.Fatalf("SearchMessages(_) = %+v/%v, want only the underscore message", underscore, err)
	}
	backslash, err := current.SearchMessages(ctx, `\`, 0)
	if err != nil || len(backslash) != 1 || !strings.Contains(backslash[0].Snippet, `\`) {
		t.Fatalf("SearchMessages(backslash) = %+v/%v, want only the backslash message", backslash, err)
	}
	if hits, err := current.SearchMessages(ctx, "complete", 0); err != nil || len(hits) != 1 {
		t.Fatalf("SearchMessages(complete) = %d/%v", len(hits), err)
	}
}

func TestSearchMessagesLimitsAndEdges(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "bulk", "Bulk"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	messages := make([]rig.Message, 0, 210)
	for index := 0; index < 210; index++ {
		messages = append(messages, rig.NewMessage(rig.RoleUser, fmt.Sprintf("needle number %d", index)))
	}
	appendText(t, current, "bulk", messages...)

	if hits := searchAll(t, current, "needle", 1000); len(hits) != maxSearchMessageLimit {
		t.Fatalf("limit 1000 hits = %d, want the %d cap", len(hits), maxSearchMessageLimit)
	}
	if hits := searchAll(t, current, "needle", 7); len(hits) != 7 {
		t.Fatalf("limit 7 hits = %d", len(hits))
	}
	if hits := searchAll(t, current, "needle", 0); len(hits) != defaultSearchMessageLimit {
		t.Fatalf("default limit hits = %d, want %d", len(hits), defaultSearchMessageLimit)
	}
	if hits := searchAll(t, current, "needle", -5); len(hits) != defaultSearchMessageLimit {
		t.Fatalf("negative limit hits = %d, want default", len(hits))
	}
	for _, query := range []string{"", "   ", "\n\t"} {
		if hits, err := current.SearchMessages(ctx, query, 0); err != nil || len(hits) != 0 {
			t.Fatalf("SearchMessages(%q) = %d/%v, want empty", query, len(hits), err)
		}
	}
	if hits, err := current.SearchMessages(ctx, "not-present-anywhere", 0); err != nil || len(hits) != 0 {
		t.Fatalf("no-match search = %d/%v", len(hits), err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := current.SearchMessages(cancelled, "needle", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search = %v, want context.Canceled", err)
	}
}

// searchAll is a helper for the limit tests.
func searchAll(t *testing.T, current *store, query string, limit int) []contracts.MessageSearchHit {
	t.Helper()
	hits, err := current.SearchMessages(context.Background(), query, limit)
	if err != nil {
		t.Fatalf("SearchMessages(%q, %d): %v", query, limit, err)
	}
	return hits
}

func TestSearchMessagesJSONOnlyMatchFallback(t *testing.T) {
	current := openTestStore(t)
	appendText(t, current, "s1", rig.Message{
		Role:      rig.RoleAssistant,
		Content:   []rig.ContentPart{rig.TextPart("plain answer")},
		ToolCalls: []rig.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"needle.txt"}`}},
	})
	hits, err := current.SearchMessages(context.Background(), "needle", 0)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search = %+v/%v, want the JSON-only match", hits, err)
	}
	if hits[0].Snippet != "plain answer" {
		t.Fatalf("fallback snippet = %q, want the decoded text", hits[0].Snippet)
	}
}

func TestSearchMessagesDeleteAndReopen(t *testing.T) {
	dataDir := t.TempDir()
	first := openTestStoreAt(t, dataDir)
	ctx := context.Background()
	if err := first.CreateSession(ctx, "keep", "Keep"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := first.CreateSession(ctx, "drop", "Drop"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	appendText(t, first, "keep", rig.NewMessage(rig.RoleUser, "shared needle keep"))
	appendText(t, first, "drop", rig.NewMessage(rig.RoleUser, "shared needle drop"))
	if err := first.DeleteSession(ctx, "drop"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if hits, err := first.SearchMessages(ctx, "needle", 0); err != nil || len(hits) != 1 || hits[0].SessionID != "keep" {
		t.Fatalf("search after delete = %+v/%v, want only keep", hits, err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := openTestStoreAt(t, dataDir)
	hits, err := reopened.SearchMessages(ctx, "needle", 0)
	if err != nil || len(hits) != 1 || hits[0].SessionID != "keep" || hits[0].SessionTitle != "Keep" {
		t.Fatalf("search after reopen = %+v/%v", hits, err)
	}
}

func TestSearchMessagesConcurrent(t *testing.T) {
	current := openTestStore(t)
	if err := current.CreateSession(context.Background(), "concurrent", "Concurrent"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for index := 0; index < 20; index++ {
		appendText(t, current, "concurrent", rig.NewMessage(rig.RoleUser, fmt.Sprintf("worker needle %d", index)))
	}
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(limit int) {
			defer waitGroup.Done()
			for iteration := 0; iteration < 10; iteration++ {
				hits, err := current.SearchMessages(context.Background(), "needle", limit)
				if err != nil {
					errs <- err
					return
				}
				if len(hits) == 0 || len(hits) > limit && limit > 0 {
					errs <- fmt.Errorf("unexpected hit count %d for limit %d", len(hits), limit)
					return
				}
			}
		}(index + 1)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestSearchMessagesSnippetLength pins the fallback snippet length.
func TestSearchMessagesSnippetLength(t *testing.T) {
	current := openTestStore(t)
	long := strings.Repeat("a", 200)
	appendText(t, current, "s1", rig.Message{
		Role:      rig.RoleAssistant,
		Content:   []rig.ContentPart{rig.TextPart(long)},
		ToolCalls: []rig.ToolCall{{ID: "c", Name: "x", Arguments: `{"q":"fallback"}`}},
	})
	hits, err := current.SearchMessages(context.Background(), "fallback", 0)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search = %+v/%v", hits, err)
	}
	if got := len([]rune(hits[0].Snippet)); got != searchSnippetFallback {
		t.Fatalf("fallback snippet has %d runes, want %d", got, searchSnippetFallback)
	}
}
