package sidebar_test

// Frame tests for the debounced background message search (2026-11 audit
// AR-02): the frame builder never queries the store synchronously, a typed
// word costs one background read after the debounce window, rapid typing
// settles on the last query only, stale results are dropped by the sequence
// guard, and disposing the module while a read is in flight is safe. The
// pattern mirrors the session Memory tab (debounce + goroutine + Post + seq).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// waitSearch pumps frames until cond holds (the background read publishes
// through UIRuntime.Post, which lands on a later frame), or fails the test.
func waitSearch(t *testing.T, tester *ui.Tester, what string, cond func() bool) {
	t.Helper()
	for attempt := 0; attempt < 400; attempt++ {
		tester.Frame()
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; texts = %v", what, tester.Texts())
}

// settleSearch advances the frame clock past the debounce window and pumps
// frames until the wanted text shows: the background read's Post lands on a
// later frame (the AR-02 fix replaced the synchronous in-build query).
func settleSearch(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	tester.Advance(250 * time.Millisecond)
	waitSearch(t, tester, "search result "+want, func() bool { return tester.HasText(want) })
}

// gatedStore is a fakeStore whose reads for the blocked queries announce
// themselves (started) and wait for the release signal, so a test can hold a
// read in flight across frames. The safety timeout keeps a broken fix from
// hanging the suite: the read returns after 3s even without a release.
type gatedStore struct {
	fakeStore
	byQuery map[string][]contracts.MessageSearchHit
	blocked map[string]bool
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedStore(byQuery map[string][]contracts.MessageSearchHit, blocked ...string) *gatedStore {
	s := &gatedStore{
		byQuery: byQuery,
		blocked: make(map[string]bool),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	for _, query := range blocked {
		s.blocked[query] = true
	}
	return s
}

func (s *gatedStore) SearchMessages(_ context.Context, query string, limit int) ([]contracts.MessageSearchHit, error) {
	s.once.Do(func() { close(s.started) })
	s.mu.Lock()
	block := s.blocked[query]
	s.mu.Unlock()
	if block {
		select {
		case <-s.release:
		case <-time.After(3 * time.Second):
		}
	}
	s.mu.Lock()
	s.calls++
	s.query = append(s.query, query)
	s.limit = append(s.limit, limit)
	hits := append([]contracts.MessageSearchHit(nil), s.byQuery[query]...)
	s.mu.Unlock()
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// readStarted reports whether the first background read has entered the store.
func (s *gatedStore) readStarted() bool {
	select {
	case <-s.started:
		return true
	default:
		return false
	}
}

// waitReadStarted pumps frames until the debounced read enters the store: the
// frame clock moved past the debounce window with Advance, and the starting
// frame runs on the next build (the Memory tab's wait idiom).
func (s *gatedStore) waitReadStarted(t *testing.T, tester *ui.Tester) {
	t.Helper()
	for attempt := 0; attempt < 400; attempt++ {
		if s.readStarted() {
			return
		}
		tester.Frame()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the debounced background read never started")
}

// TestMessageSearchDebouncedAsync pins the AR-02 fix: typing schedules one
// background read after the debounce window instead of a synchronous store
// query inside the build frame, and the hits arrive through Post.
func TestMessageSearchDebouncedAsync(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "项目讨论", "user", "如何修复登录问题", now),
	}}
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()
	tester.Frame()
	if calls := store.callCount(); calls != 0 {
		t.Fatalf("SearchMessages ran %d times inside the typing frames, want 0 (debounced background read)", calls)
	}

	tester.Advance(250 * time.Millisecond)
	waitSearch(t, tester, "async search hits", func() bool {
		return tester.HasText("如何修复登录问题") && store.callCount() == 1
	})
	if query := store.lastQuery(); query != "登录" {
		t.Fatalf("SearchMessages query = %q, want 登录", query)
	}
	if limit := store.lastLimit(); limit != 8 {
		t.Fatalf("SearchMessages limit = %d, want 8", limit)
	}
	// The cached result serves further frames without another read.
	tester.Frame()
	tester.Frame()
	if calls := store.callCount(); calls != 1 {
		t.Fatalf("SearchMessages calls after cached frames = %d, want 1", calls)
	}
}

// TestMessageSearchRapidTypingOneRead pins the debounce coalescing: a burst of
// keystrokes inside the window costs one read for the final query, not one
// per character.
func TestMessageSearchRapidTypingOneRead(t *testing.T) {
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "项目讨论", "user", "如何修复登录问题", time.Unix(1700000000, 0)),
	}}
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登")
	tester.Frame()
	tester.Type("录")
	tester.Frame()
	if calls := store.callCount(); calls != 0 {
		t.Fatalf("SearchMessages ran %d times during the typing burst, want 0", calls)
	}
	tester.Advance(250 * time.Millisecond)
	waitSearch(t, tester, "single debounced read", func() bool {
		return store.callCount() == 1 && tester.HasText("如何修复登录问题")
	})
	if query := store.lastQuery(); query != "登录" {
		t.Fatalf("SearchMessages query = %q, want the final query 登录", query)
	}
	// Nothing more is read while the result stays valid.
	tester.Advance(500 * time.Millisecond)
	for frame := 0; frame < 3; frame++ {
		tester.Frame()
	}
	if calls := store.callCount(); calls != 1 {
		t.Fatalf("SearchMessages calls after the debounce = %d, want one", calls)
	}
}

// TestMessageSearchReadingState pins the pending-read feedback: while the
// query's read has not landed, the message section says it is searching and
// the overall "no matches" state waits for the result instead of flashing.
func TestMessageSearchReadingState(t *testing.T) {
	t.Run("searching line while pending", func(t *testing.T) {
		store := &fakeStore{hits: []contracts.MessageSearchHit{
			hit("s1", "项目讨论", "user", "如何修复登录问题", time.Unix(1700000000, 0)),
		}}
		h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "搜索会话与消息")
		tester.Type("登录")
		tester.Frame()
		h.assertTexts(tester, "搜索中…")
		h.assertNoTexts(tester, "如何修复登录问题", "没有匹配")
		tester.Advance(250 * time.Millisecond)
		waitSearch(t, tester, "hits replace the searching line", func() bool {
			return tester.HasText("如何修复登录问题")
		})
		h.assertNoTexts(tester, "搜索中…")
	})

	t.Run("no premature overall empty", func(t *testing.T) {
		h := newHarnessWithStore(t, searchChat(), &fakeStore{}, sidebar.Config{})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "搜索会话与消息")
		tester.Type("不存在")
		tester.Frame()
		// Neither a title nor a message matched yet — but the message read is
		// still pending, so the one overall empty state must wait for it.
		h.assertNoTexts(tester, "没有匹配")
		tester.Advance(250 * time.Millisecond)
		waitSearch(t, tester, "overall empty after the read", func() bool {
			return tester.HasText("没有匹配")
		})
	})
}

// TestMessageSearchStaleReadDropped pins the sequence guard: a slow read for
// an abandoned query never replaces the visible results of the newer query.
func TestMessageSearchStaleReadDropped(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := newGatedStore(map[string][]contracts.MessageSearchHit{
		"登录": {hit("s1", "项目讨论", "user", "登录旧结果片段", now)},
		"认证": {hit("s1", "项目讨论", "user", "认证新结果片段", now)},
	}, "登录")
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()
	if store.readStarted() {
		t.Fatal("the store read started inside the build frame, want a debounced background read")
	}
	tester.Advance(250 * time.Millisecond)
	store.waitReadStarted(t, tester)

	// Abandon the slow query for a new one; the new read settles first.
	clickLabel(t, tester, "搜索会话与消息")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("认证")
	tester.Frame()
	tester.Advance(250 * time.Millisecond)
	waitSearch(t, tester, "new query results", func() bool {
		return tester.HasText("认证新结果片段")
	})
	// Only the new query's read has completed; the abandoned one is still in
	// flight (the gated store counts a read when it returns).
	if calls := store.callCount(); calls != 1 || store.lastQuery() != "认证" {
		t.Fatalf("completed reads = %d (last %q), want only 认证 settled so far", calls, store.lastQuery())
	}

	// The abandoned read finishes late: its result must be dropped, not shown.
	close(store.release)
	for frame := 0; frame < 20; frame++ {
		tester.Frame()
		time.Sleep(time.Millisecond)
	}
	if calls := store.callCount(); calls != 2 {
		t.Fatalf("SearchMessages calls = %d, want 2 (one per query)", calls)
	}
	h.assertTexts(tester, "认证新结果片段")
	h.assertNoTexts(tester, "登录旧结果片段")
}

// TestMessageSearchDisposeDuringRead pins teardown safety: disposing the
// module while a background read is in flight neither panics nor applies the
// late result (the sequence guard drops it after the dispose cleanup).
func TestMessageSearchDisposeDuringRead(t *testing.T) {
	store := newGatedStore(map[string][]contracts.MessageSearchHit{
		"登录": {hit("s1", "项目讨论", "user", "如何修复登录问题", time.Unix(1700000000, 0))},
	}, "登录")
	chat := searchChat()
	h := newHarnessWithStore(t, chat, store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()
	if store.readStarted() {
		t.Fatal("the store read started inside the build frame, want a debounced background read")
	}
	tester.Advance(250 * time.Millisecond)
	store.waitReadStarted(t, tester)

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose sidebar module: %v", err)
	}
	if !chat.isUnsubscribed() {
		t.Fatal("chat subscription was not unwound on disposal")
	}
	// The in-flight read completes after disposal: its Post must be a quiet
	// no-op, not a panic and not a resurrected result.
	close(store.release)
	for frame := 0; frame < 30; frame++ {
		tester.Frame()
		time.Sleep(time.Millisecond)
	}
	h.assertNoTexts(tester, "新对话", "如何修复登录问题")
}
