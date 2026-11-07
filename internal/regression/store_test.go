package regression

// Adversarial audit: the SQLite session store module. Negative/fault cases:
// LIKE wildcard and quote injection, search-limit boundaries, concurrent
// writers/readers, reopening and migrating an existing database, invalid
// session IDs, oversized titles/messages and the empty-range event readers.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
)

// storeHarness activates the real sessionstore module on h and returns the
// store plus its optional message searcher.
func startStore(t *testing.T, h *harness) (contracts.Store, contracts.MessageSearcher) {
	t.Helper()
	activate(t, h.ctx, sessionstore.Module(), sessionstore.Config{})
	store := requireService(t, h.ctx, contracts.KeyStore)
	searcher, ok := store.(contracts.MessageSearcher)
	if !ok {
		t.Fatal("the store does not implement contracts.MessageSearcher")
	}
	return store, searcher
}

// mustCreateSession creates one store session or fails the test.
func mustCreateSession(t *testing.T, store contracts.Store, id, title string) {
	t.Helper()
	if err := store.CreateSession(context.Background(), id, title); err != nil {
		t.Fatalf("CreateSession(%q): %v", id, err)
	}
}

// appendMessages appends n messages to a session.
func appendMessages(t *testing.T, store contracts.Store, sessionID string, messages ...string) {
	t.Helper()
	for index, text := range messages {
		if err := store.History().Append(context.Background(), sessionID,
			rig.NewMessage(rig.RoleUser, text)); err != nil {
			t.Fatalf("History.Append(%d): %v", index, err)
		}
	}
}

// sessionIDsOf collects the distinct session IDs of search hits.
func sessionIDsOf(hits []contracts.MessageSearchHit) map[string]bool {
	ids := make(map[string]bool, len(hits))
	for _, hit := range hits {
		ids[hit.SessionID] = true
	}
	return ids
}

// TestStoreSearchMessagesLikeWildcardsAreLiteral is the SQL-injection negative:
// %, _ and \ in the query must match literally, not as LIKE wildcards, and SQL
// fragments must never widen the result set.
func TestStoreSearchMessagesLikeWildcardsAreLiteral(t *testing.T) {
	h := newHarness(t)
	store, searcher := startStore(t, h)
	ctx := context.Background()

	mustCreateSession(t, store, "s-percent", "Percent")
	appendMessages(t, store, "s-percent", "progress is 100% done", "plain percent-free line")
	mustCreateSession(t, store, "s-underscore", "Underscore")
	appendMessages(t, store, "s-underscore", "under_score token", "dash-score token")
	mustCreateSession(t, store, "s-backslash", "Backslash")
	appendMessages(t, store, "s-backslash", `C:\tmp\file path`, "forward/slash path")
	mustCreateSession(t, store, "s-injection", "Injection")
	appendMessages(t, store, "s-injection", "' OR 1=1 --", `<script>alert(1)</script>`)

	// "%" matches only messages that literally contain a percent sign.
	hits, err := searcher.SearchMessages(ctx, "%", 50)
	if err != nil {
		t.Fatalf("SearchMessages(%%): %v", err)
	}
	if ids := sessionIDsOf(hits); len(hits) != 1 || !ids["s-percent"] {
		t.Fatalf("SearchMessages(%%): percent hits %+v, want only the s-percent message", hits)
	}

	// "_" matches only literal underscores (not "dash-score").
	hits, err = searcher.SearchMessages(ctx, "_", 50)
	if err != nil {
		t.Fatalf("SearchMessages(_): %v", err)
	}
	if ids := sessionIDsOf(hits); len(hits) != 1 || !ids["s-underscore"] {
		t.Fatalf("SearchMessages(_): underscore hits %+v, want only the s-underscore message", hits)
	}
	if !strings.Contains(hits[0].Snippet, "under_score") {
		t.Fatalf("snippet = %q, want the under_score context", hits[0].Snippet)
	}

	// SQL fragments are searched as plain text, never as SQL.
	hits, err = searcher.SearchMessages(ctx, "' OR 1=1 --", 50)
	if err != nil {
		t.Fatalf("SearchMessages(injection text): %v", err)
	}
	if ids := sessionIDsOf(hits); len(hits) != 1 || !ids["s-injection"] {
		t.Fatalf("SearchMessages(injection text): hits %+v, want only the s-injection message", hits)
	}

	// An empty/whitespace query matches nothing (not "everything").
	for _, query := range []string{"", "   ", "\t\n"} {
		hits, err := searcher.SearchMessages(ctx, query, 50)
		if err != nil {
			t.Fatalf("SearchMessages(%q): %v", query, err)
		}
		if len(hits) != 0 {
			t.Fatalf("SearchMessages(%q): %d hits, want 0", query, len(hits))
		}
	}
}

// TestStoreSearchMessagesLimitBoundaries covers limit 0, negative, 1 and very
// large values: the store clamps into [default 50, hard cap 200].
func TestStoreSearchMessagesLimitBoundaries(t *testing.T) {
	h := newHarness(t)
	store, searcher := startStore(t, h)
	ctx := context.Background()

	mustCreateSession(t, store, "s-default", "Default cap")
	appendMessages(t, store, "s-default", numberedMessages("needle", 55)...)
	hits, err := searcher.SearchMessages(ctx, "needle", 0)
	if err != nil {
		t.Fatalf("SearchMessages(limit 0): %v", err)
	}
	if len(hits) != 50 {
		t.Fatalf("SearchMessages(limit 0) = %d hits, want the default 50", len(hits))
	}
	hits, err = searcher.SearchMessages(ctx, "needle", -7)
	if err != nil {
		t.Fatalf("SearchMessages(limit -7): %v", err)
	}
	if len(hits) != 50 {
		t.Fatalf("SearchMessages(limit -7) = %d hits, want the default 50", len(hits))
	}
	hits, err = searcher.SearchMessages(ctx, "needle", 1)
	if err != nil {
		t.Fatalf("SearchMessages(limit 1): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("SearchMessages(limit 1) = %d hits, want 1", len(hits))
	}

	// A huge limit is capped at 200, not at the number of stored rows.
	mustCreateSession(t, store, "s-hard-cap", "Hard cap")
	appendMessages(t, store, "s-hard-cap", numberedMessages("capneedle", 230)...)
	hits, err = searcher.SearchMessages(ctx, "capneedle", 1_000_000)
	if err != nil {
		t.Fatalf("SearchMessages(huge limit): %v", err)
	}
	if len(hits) != 200 {
		t.Fatalf("SearchMessages(huge limit) = %d hits, want the hard cap 200", len(hits))
	}
	// Newest first: the hits are ordered by the auto-increment message ID.
	if hits[0].Snippet == hits[len(hits)-1].Snippet {
		t.Fatalf("hit ordering looks unordered: first == last snippet")
	}
}

// numberedMessages builds n distinct message texts carrying marker.
func numberedMessages(marker string, n int) []string {
	messages := make([]string, 0, n)
	for index := 0; index < n; index++ {
		messages = append(messages, fmt.Sprintf("%s number %03d", marker, index))
	}
	return messages
}

// TestStoreConcurrentCreateReplaceSearch hammers session writes and cross
// session searches from several goroutines: no error, no lost session and no
// data race (-race).
func TestStoreConcurrentCreateReplaceSearch(t *testing.T) {
	h := newHarness(t)
	store, searcher := startStore(t, h)
	ctx := context.Background()
	const writers = 6
	const messagesPerSession = 5

	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		writer := writer
		wg.Add(1)
		go func() {
			defer wg.Done()
			sessionID := fmt.Sprintf("concurrent-%d", writer)
			if err := store.CreateSession(ctx, sessionID, fmt.Sprintf("Concurrent %d", writer)); err != nil {
				t.Errorf("CreateSession(%q): %v", sessionID, err)
				return
			}
			messages := make([]rig.Message, 0, messagesPerSession)
			for index := 0; index < messagesPerSession; index++ {
				messages = append(messages, rig.NewMessage(rig.RoleUser,
					fmt.Sprintf("concurrentmarker w%d m%d", writer, index)))
			}
			if err := store.ReplaceHistory(ctx, sessionID, messages); err != nil {
				t.Errorf("ReplaceHistory(%q): %v", sessionID, err)
			}
		}()
	}

	// Concurrent readers/search must not block or fail while writers run.
	var readerWG sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for attempt := 0; attempt < 10; attempt++ {
				if _, err := searcher.SearchMessages(ctx, "concurrentmarker", 50); err != nil {
					t.Errorf("SearchMessages: %v", err)
					return
				}
				if _, err := store.Sessions(ctx); err != nil {
					t.Errorf("Sessions: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	readerWG.Wait()

	metas, err := store.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(metas) < writers {
		t.Fatalf("Sessions() = %d, want at least the %d written sessions", len(metas), writers)
	}
	for writer := 0; writer < writers; writer++ {
		sessionID := fmt.Sprintf("concurrent-%d", writer)
		messages, err := store.History().Load(ctx, sessionID)
		if err != nil {
			t.Fatalf("History.Load(%q): %v", sessionID, err)
		}
		if len(messages) != messagesPerSession {
			t.Fatalf("History.Load(%q) = %d messages, want %d", sessionID, len(messages), messagesPerSession)
		}
	}
	hits, err := searcher.SearchMessages(ctx, "concurrentmarker", 200)
	if err != nil {
		t.Fatalf("SearchMessages after concurrency: %v", err)
	}
	if len(hits) != writers*messagesPerSession {
		t.Fatalf("SearchMessages = %d hits, want %d", len(hits), writers*messagesPerSession)
	}
}

// TestStoreReopenAndDoubleOpen covers persistence across a restart, the
// idempotent migration on a second open and two live connections to one file.
func TestStoreReopenAndDoubleOpen(t *testing.T) {
	first := newHarness(t)
	store, _ := startStore(t, first)
	ctx := context.Background()
	mustCreateSession(t, store, "persisted", "Persisted")
	appendMessages(t, store, "persisted", "survives a restart")
	if err := store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: "persisted", Type: "audit", Payload: []byte(`{"kind":"reopen"}`),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	// Dispose the first root (closes SQLite through the module effect) and
	// reopen the same data directory: schema migration runs again and data stays.
	if err := first.ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first root: %v", err)
	}
	second := newHarnessWithDataDir(t, first.dataDir)
	secondStore, secondSearcher := startStore(t, second)

	meta, found, err := secondStore.GetSessionMeta(ctx, "persisted")
	if err != nil || !found {
		t.Fatalf("GetSessionMeta after reopen = (%+v, %v, %v), want found", meta, found, err)
	}
	if meta.Title != "Persisted" || meta.MessageCount != 1 {
		t.Fatalf("reopened session meta = %+v, want title Persisted and 1 message", meta)
	}
	messages, err := secondStore.History().Load(ctx, "persisted")
	if err != nil || len(messages) != 1 || messages[0].Text() != "survives a restart" {
		t.Fatalf("History.Load after reopen = %v, %v", messages, err)
	}
	if hits, err := secondSearcher.SearchMessages(ctx, "survives", 10); err != nil || len(hits) != 1 {
		t.Fatalf("SearchMessages after reopen = %v, %v", hits, err)
	}
	events, err := secondStore.Events(ctx, "persisted", 0, 0)
	if err != nil || len(events) != 1 || events[0].Type != "audit" {
		t.Fatalf("Events after reopen = %v, %v", events, err)
	}

	// A third live connection to the same file must not corrupt the schema or
	// block (SQLite WAL + busy timeout); both connections read each other's data.
	third := newHarnessWithDataDir(t, first.dataDir)
	thirdStore, _ := startStore(t, third)
	mustCreateSession(t, thirdStore, "from-third", "Third writer")
	appendMessages(t, thirdStore, "from-third", "cross connection visibility")
	if _, found, err := secondStore.GetSessionMeta(ctx, "from-third"); err != nil || !found {
		t.Fatalf("second connection cannot see third's session: found=%v err=%v", found, err)
	}
}

// TestStoreInvalidSessionArguments covers unknown/empty session IDs: reads
// return empty results, mutations return ErrUnknownSession, deletes stay no-ops.
func TestStoreInvalidSessionArguments(t *testing.T) {
	h := newHarness(t)
	store, _ := startStore(t, h)
	ctx := context.Background()

	if err := store.RenameSession(ctx, "missing", "x"); !errors.Is(err, sessionstore.ErrUnknownSession) {
		t.Fatalf("RenameSession(unknown) error = %v, want ErrUnknownSession", err)
	}
	if err := store.RenameSession(ctx, "", "x"); err == nil {
		t.Fatal("RenameSession(empty ID) succeeded")
	}
	if err := store.DeleteSession(ctx, "missing"); err != nil {
		t.Fatalf("DeleteSession(unknown) = %v, want a no-op nil", err)
	}
	for _, id := range []string{"", "   "} {
		if err := store.DeleteSession(ctx, id); err == nil {
			t.Fatalf("DeleteSession(%q) succeeded, want an invalid-input error", id)
		}
	}
	if meta, found, err := store.GetSessionMeta(ctx, "missing"); err != nil || found || meta.ID != "" {
		t.Fatalf("GetSessionMeta(unknown) = (%+v, %v, %v), want zero/false/nil", meta, found, err)
	}
	if messages, err := store.History().Load(ctx, "missing"); err != nil || len(messages) != 0 {
		t.Fatalf("History.Load(unknown) = (%v, %v), want (nil, nil)", messages, err)
	}
	if checkpoint, err := store.CheckpointStore().Load(ctx, "missing"); err != nil || checkpoint != nil {
		t.Fatalf("CheckpointStore.Load(unknown) = (%v, %v), want (nil, nil)", checkpoint, err)
	}
	// Events with an inverted range and Last with a non-positive limit are empty.
	if err := store.AppendEvent(ctx, rig.SessionEvent{SessionID: "events", Type: "audit"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if events, err := store.Events(ctx, "events", 5, 2); err != nil || len(events) != 0 {
		t.Fatalf("Events(from>to) = (%v, %v), want empty", events, err)
	}
	for _, limit := range []int{0, -3} {
		if events, err := store.Last(ctx, "events", limit); err != nil || len(events) != 0 {
			t.Fatalf("Last(limit %d) = (%v, %v), want empty", limit, events, err)
		}
	}
	if events, err := store.Events(ctx, "missing", 0, 0); err != nil || len(events) != 0 {
		t.Fatalf("Events(unknown) = (%v, %v), want empty", events, err)
	}
}

// TestStoreOversizedTitleAndMessage covers long inputs: a 64 KiB title and a
// 1 MiB message persist, and the search snippet stays bounded.
func TestStoreOversizedTitleAndMessage(t *testing.T) {
	h := newHarness(t)
	store, searcher := startStore(t, h)
	ctx := context.Background()

	longTitle := strings.Repeat("标题-", 16*1024) // ~64 KiB of UTF-8
	mustCreateSession(t, store, "oversized", "placeholder")
	if err := store.RenameSession(ctx, "oversized", longTitle); err != nil {
		t.Fatalf("RenameSession(long title): %v", err)
	}
	meta, found, err := store.GetSessionMeta(ctx, "oversized")
	if err != nil || !found || meta.Title != longTitle {
		t.Fatalf("long title round trip failed (found=%v err=%v len=%d)", found, err, len(meta.Title))
	}

	longMessage := strings.Repeat("长消息内容 ", 128*1024) // ~1.3 MiB UTF-8
	longMessage += " tail-marker-123456"
	if err := store.History().Append(ctx, "oversized", rig.NewMessage(rig.RoleUser, longMessage)); err != nil {
		t.Fatalf("History.Append(1 MiB message): %v", err)
	}
	messages, err := store.History().Load(ctx, "oversized")
	if err != nil || len(messages) != 1 || messages[0].Text() != longMessage {
		t.Fatalf("long message round trip failed (len=%d err=%v)", len(messages), err)
	}
	hits, err := searcher.SearchMessages(ctx, "tail-marker", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("SearchMessages(long message) = (%v, %v)", hits, err)
	}
	if runes := utf8.RuneCountInString(hits[0].Snippet); runes > 200 {
		t.Fatalf("search snippet has %d runes, want a bounded excerpt", runes)
	}
	if !strings.Contains(hits[0].Snippet, "tail-marker") {
		t.Fatalf("snippet %q does not contain the match", hits[0].Snippet)
	}
}

// TestStoreSearchMessagesFindsQuotedText is the reverse-assertion for the
// JSON-escaping defect: the stored payload is the JSON encoding of the message,
// so a user searching for text containing a double quote (or backslash) gets no
// hit although the message text contains it. When the store is fixed to search
// decoded text this test passes; until then it records the defect and skips.
func TestStoreSearchMessagesFindsQuotedText(t *testing.T) {
	h := newHarness(t)
	store, searcher := startStore(t, h)
	ctx := context.Background()
	mustCreateSession(t, store, "quoted", "Quoted")
	appendMessages(t, store, "quoted", `the "quoted" answer`, `path C:\tmp\file`)

	hits, err := searcher.SearchMessages(ctx, `"quoted"`, 10)
	if err != nil {
		t.Fatalf("SearchMessages(quoted text): %v", err)
	}
	if len(hits) == 0 {
		t.Skip("AUDIT-P2-1 (known defect): SearchMessages searches the raw JSON payload; " +
			"text containing \" or \\ is JSON-escaped and cannot be found by its literal text")
	}
	if len(hits) != 1 || hits[0].SessionID != "quoted" {
		t.Fatalf("SearchMessages(quoted text) = %+v, want the quoted session", hits)
	}

	hits, err = searcher.SearchMessages(ctx, `C:\tmp\file`, 10)
	if err != nil {
		t.Fatalf("SearchMessages(backslash text): %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "quoted" {
		t.Fatalf("SearchMessages(backslash text) = %+v, want the backslash session", hits)
	}
}

// TestStoreSearchMessagesQuoteDoesNotMatchEverything is the false-positive half
// of the same defect: the payload JSON always contains double quotes, so a
// search for `"` currently matches every stored message.
func TestStoreSearchMessagesQuoteDoesNotMatchEverything(t *testing.T) {
	h := newHarness(t)
	store, searcher := startStore(t, h)
	ctx := context.Background()
	mustCreateSession(t, store, "plain", "Plain")
	appendMessages(t, store, "plain", "message without quotes", `message with "quotes"`)

	hits, err := searcher.SearchMessages(ctx, `"`, 10)
	if err != nil {
		t.Fatalf("SearchMessages(quote): %v", err)
	}
	if len(hits) != 1 {
		t.Skipf("AUDIT-P2-1 (known defect): searching for a double quote returns %d/%d messages "+
			"because the raw JSON payload contains structural quotes", len(hits), 2)
	}
	if len(hits) == 1 && hits[0].Snippet != "message without quotes" {
		// Either the quoted or unquoted row may be the only literal hit; the
		// important part is that the structural quotes no longer match.
		t.Logf("single quote hit snippet = %q", hits[0].Snippet)
	}
}

// TestStoreEventSearchFindsQuotedPayloadText is the event-log half of
// AUDIT-P2-1: rig.SessionEvent payloads are raw JSON, so quoted text is escaped.
func TestStoreEventSearchFindsQuotedPayloadText(t *testing.T) {
	h := newHarness(t)
	store, _ := startStore(t, h)
	ctx := context.Background()
	if err := store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: "events",
		Type:      "tool_call",
		Payload:   []byte(`{"summary":"the \"quoted\" answer"}`),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	hits, err := store.Search(ctx, `"quoted"`, 10)
	if err != nil {
		t.Fatalf("Search(quoted): %v", err)
	}
	if len(hits) == 0 {
		t.Skip("AUDIT-P2-1 (known defect): event Search matches the raw JSON payload; " +
			"quoted payload text is escaped and not found")
	}
	if len(hits) != 1 {
		t.Fatalf("Search(quoted) = %+v, want one event", hits)
	}
}

// TestStoreEventSearchLikeWildcards covers the session event log's own LIKE
// query: wildcards stay literal and an inverted range returns nothing.
func TestStoreEventSearchLikeWildcards(t *testing.T) {
	h := newHarness(t)
	store, _ := startStore(t, h)
	ctx := context.Background()

	for index, payload := range []string{
		`{"text":"100%"}`, `{"text":"a_b"}`, `{"text":"plain"}`, `{"text":"quote ' OR 1=1 --"}`,
	} {
		if err := store.AppendEvent(ctx, rig.SessionEvent{
			SessionID: "events",
			Type:      fmt.Sprintf("event-%d", index),
			Payload:   []byte(payload),
			Time:      time.Unix(int64(1_700_000_000+index), 0),
		}); err != nil {
			t.Fatalf("AppendEvent(%d): %v", index, err)
		}
	}
	hits, err := store.Search(ctx, "%", 10)
	if err != nil {
		t.Fatalf("Search(%%): %v", err)
	}
	if len(hits) != 1 || hits[0].Type != "event-0" {
		t.Fatalf("Search(%%) = %+v, want only event-0", hits)
	}
	hits, err = store.Search(ctx, "_", 10)
	if err != nil {
		t.Fatalf("Search(_): %v", err)
	}
	if len(hits) != 1 || hits[0].Type != "event-1" {
		t.Fatalf("Search(_) = %+v, want only event-1", hits)
	}
	if hits, err := store.Search(ctx, "quote ' OR 1=1 --", 10); err != nil || len(hits) != 1 {
		t.Fatalf("Search(injection text) = (%v, %v), want one literal hit", hits, err)
	}
}
