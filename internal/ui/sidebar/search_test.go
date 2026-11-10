package sidebar_test

// Tests for the sidebar's full-text message search: hits render below the
// title-filtered list, a click opens the session, empty/error states, the
// per-query cache and the no-searcher fallback.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// fakeStore implements contracts.Store (stubs) plus contracts.MessageSearcher
// with scripted hits, an optional error and call accounting.
type fakeStore struct {
	mu sync.Mutex

	hits  []contracts.MessageSearchHit
	err   error
	calls int
	query []string
	limit []int
}

func (s *fakeStore) SearchMessages(_ context.Context, query string, limit int) ([]contracts.MessageSearchHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.query = append(s.query, query)
	s.limit = append(s.limit, limit)
	if s.err != nil {
		return nil, s.err
	}
	hits := append([]contracts.MessageSearchHit(nil), s.hits...)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (s *fakeStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeStore) lastQuery() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.query) == 0 {
		return ""
	}
	return s.query[len(s.query)-1]
}

func (s *fakeStore) lastLimit() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.limit) == 0 {
		return 0
	}
	return s.limit[len(s.limit)-1]
}

// contracts.Store stubs (the sidebar never calls them; only the search half is
// exercised here).

func (s *fakeStore) Events(context.Context, string, int64, int64) ([]rig.SessionEvent, error) {
	return nil, nil
}

func (s *fakeStore) Search(context.Context, string, int) ([]rig.SessionEvent, error) {
	return nil, nil
}

func (s *fakeStore) Last(context.Context, string, int) ([]rig.SessionEvent, error) {
	return nil, nil
}

func (s *fakeStore) History() rig.History { return nil }

func (s *fakeStore) CheckpointStore() rig.CheckpointStore { return nil }

func (s *fakeStore) ReplaceHistory(context.Context, string, []rig.Message) error { return nil }

func (s *fakeStore) CreateSession(context.Context, string, string) error { return nil }

func (s *fakeStore) DeleteSession(context.Context, string) error { return nil }

func (s *fakeStore) RenameSession(context.Context, string, string) error { return nil }

func (s *fakeStore) Sessions(context.Context) ([]contracts.SessionMeta, error) { return nil, nil }

func (s *fakeStore) GetSessionMeta(context.Context, string) (contracts.SessionMeta, bool, error) {
	return contracts.SessionMeta{}, false, nil
}

func (s *fakeStore) AppendEvent(context.Context, rig.SessionEvent) error { return nil }

// --- helpers ----------------------------------------------------------------

func searchChat() *fakeChat {
	return &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "项目讨论"},
			{ID: "s2", Title: "另一个会话"},
		},
		active: "s1",
	}
}

func hit(sessionID, title, role, snippet string, created time.Time) contracts.MessageSearchHit {
	return contracts.MessageSearchHit{
		SessionID:    sessionID,
		SessionTitle: title,
		Role:         role,
		Snippet:      snippet,
		CreatedAt:    created,
	}
}

// --- tests ------------------------------------------------------------------

func TestMessageSearchRendersHits(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "项目讨论", "user", "如何修复登录问题", now),
		hit("s1", "项目讨论", "assistant", "先检查认证模块", now.Add(-time.Minute)),
	}}
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()

	h.assertTexts(tester, "消息搜索", "项目讨论", "用户", "助手",
		"如何修复登录问题", "先检查认证模块",
		now.Local().Format("01-02 15:04"))
	// The hits are an official List: named, with the hit rows exposed by name.
	if _, ok := tester.AccessByRole(ui.RoleList, "消息搜索"); !ok {
		t.Fatalf("message-search hits are not a named list; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "项目讨论 如何修复登录问题"); !ok {
		t.Fatalf("message-search rows are not exposed by name; texts = %v", tester.Texts())
	}
	if calls := store.callCount(); calls != 1 {
		t.Fatalf("SearchMessages calls = %d, want 1", calls)
	}
	if query := store.lastQuery(); query != "登录" {
		t.Fatalf("SearchMessages query = %q, want 登录", query)
	}
	if limit := store.lastLimit(); limit != 8 {
		t.Fatalf("SearchMessages limit = %d, want 8", limit)
	}
}

func TestMessageSearchClickOpensSession(t *testing.T) {
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s2", "另一个会话", "user", "这里提到登录修复", time.Unix(1700000000, 0)),
	}}
	chat := searchChat()
	h := newHarnessWithStore(t, chat, store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()

	if err := tester.Click("这里提到登录修复"); err != nil {
		t.Fatalf("click hit row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := chat.activeCalls(); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("SetActiveSession calls = %v, want [s2]", got)
	}
}

func TestMessageSearchEmptyAndError(t *testing.T) {
	t.Run("no hits", func(t *testing.T) {
		h := newHarnessWithStore(t, searchChat(), &fakeStore{}, sidebar.Config{})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "搜索会话与消息")
		tester.Type("不存在")
		tester.Frame()
		h.assertTexts(tester, "消息搜索", "没有匹配的消息")
	})

	t.Run("error", func(t *testing.T) {
		store := &fakeStore{err: errors.New("fts unavailable")}
		h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "搜索会话与消息")
		tester.Type("登录")
		tester.Frame()
		h.assertTexts(tester, "消息搜索", "搜索消息失败：fts unavailable")
	})
}

func TestMessageSearchShownWhenTitlesDoNotMatch(t *testing.T) {
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "项目讨论", "user", "登录相关讨论", time.Unix(1700000000, 0)),
	}}
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()

	// No session title matches "登录", but the message search still renders.
	h.assertTexts(tester, "没有匹配的会话", "消息搜索", "登录相关讨论")
}

func TestMessageSearchQueryCache(t *testing.T) {
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "项目讨论", "user", "登录相关讨论", time.Unix(1700000000, 0)),
	}}
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("登录")
	tester.Frame()
	if calls := store.callCount(); calls != 1 {
		t.Fatalf("SearchMessages calls = %d, want 1", calls)
	}

	// Repeated frames with the same query reuse the cached hits.
	tester.Frame()
	tester.Frame()
	if calls := store.callCount(); calls != 1 {
		t.Fatalf("SearchMessages calls after extra frames = %d, want 1", calls)
	}

	// A new query searches again; clearing the field hides the section.
	h.click(tester, "搜索会话与消息")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("认证")
	tester.Frame()
	if calls := store.callCount(); calls != 2 || store.lastQuery() != "认证" {
		t.Fatalf("calls = %d last query = %q, want 2/认证", store.callCount(), store.lastQuery())
	}
	// Clearing the field hides the section again.
	h.click(tester, "搜索会话与消息")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.assertNoTexts(tester, "消息搜索")
}

func TestMessageSearchLimit(t *testing.T) {
	hits := make([]contracts.MessageSearchHit, 0, 10)
	for index := 1; index <= 10; index++ {
		hits = append(hits, hit("s1", "项目讨论", "user", "命中片段"+strings.Repeat("x", index), time.Unix(int64(1700000000+index), 0)))
	}
	store := &fakeStore{hits: hits}
	h := newHarnessWithStore(t, searchChat(), store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("片段")
	tester.Frame()
	if limit := store.lastLimit(); limit != 8 {
		t.Fatalf("SearchMessages limit = %d, want 8", limit)
	}
	frame := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(frame, "命中片段xxxxxxxx") {
		t.Fatalf("8th hit row missing; texts = %v", tester.Texts())
	}
	if strings.Contains(frame, "命中片段xxxxxxxxx") {
		t.Fatalf("a 9th hit row was rendered past the limit; texts = %v", tester.Texts())
	}
}

// TestMessageSearchHidesAutomationRunHits pins the same user-only rule for the
// durable message search: hits from automation-run conversations never render,
// whether the run session is still in the read model or only the stored hit
// remains (the hit carries the run's title prefix).
func TestMessageSearchHidesAutomationRunHits(t *testing.T) {
	now := time.Unix(1700000000, 0)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "普通会话"},
		{ID: "s2", Title: "自动化: 每日摘要"},
	}}
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "普通会话", "user", "可见命中片段", now),
		hit("s2", "自动化: 每日摘要", "assistant", "自动化命中片段", now),
		// The run session is gone from the read model, but the stored hit still
		// names it: the title prefix keeps it hidden.
		hit("s-old", "自动化: 历史任务", "user", "历史命中片段", now),
		// A run hit whose stored title is empty is hidden by the read model's
		// session-ID set.
		hit("s2", "", "assistant", "无标题命中片段", now),
	}}
	h := newHarnessWithStore(t, chat, store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("命中片段")
	tester.Frame()

	h.assertTexts(tester, "消息搜索", "可见命中片段")
	h.assertNoTexts(tester, "自动化: 每日摘要", "自动化命中片段",
		"历史命中片段", "无标题命中片段", "未命名会话")
}

func TestSearchWithoutSearcherKeepsTitleFilter(t *testing.T) {
	chat := searchChat()
	h := newHarness(t, chat, sidebar.Config{}) // no KeyStore provided
	tester := h.tester()
	tester.Frame()
	h.click(tester, "搜索会话与消息")
	tester.Type("另一")
	tester.Frame()
	h.assertTexts(tester, "另一个会话")
	h.assertNoTexts(tester, "项目讨论", "消息搜索")
}
