package session_test

// Tests for the Memory tab: the real memory store integration (list, search,
// add, remove, errors) and the assistant Markdown rendering.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeMemoryStore is an in-memory contracts.MemoryStore with scripted errors.
type fakeMemoryStore struct {
	mu sync.Mutex

	items  []contracts.Memory
	nextID int

	listCalls    int
	searchCalls  []string
	addCalls     []contracts.Memory
	removeCalls  []string
	listErr      error
	searchErr    error
	addErr       error
	removeFails  bool
	addCreatedAt time.Time
}

func newFakeMemoryStore(memories ...contracts.Memory) *fakeMemoryStore {
	return &fakeMemoryStore{
		items:        append([]contracts.Memory(nil), memories...),
		nextID:       len(memories),
		addCreatedAt: time.Unix(1700000000, 0),
	}
}

func (s *fakeMemoryStore) List(context.Context) ([]contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]contracts.Memory(nil), s.items...), nil
}

func (s *fakeMemoryStore) Search(_ context.Context, query string) ([]contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchCalls = append(s.searchCalls, query)
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	needle := strings.ToLower(query)
	found := make([]contracts.Memory, 0, len(s.items))
	for _, memory := range s.items {
		if strings.Contains(strings.ToLower(memory.Text), needle) {
			found = append(found, memory)
		}
	}
	return found, nil
}

func (s *fakeMemoryStore) Add(_ context.Context, memory contracts.Memory) (contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addCalls = append(s.addCalls, memory)
	if s.addErr != nil {
		return contracts.Memory{}, s.addErr
	}
	s.nextID++
	memory.ID = fmt.Sprintf("mem-%d", s.nextID)
	if memory.Created.IsZero() {
		memory.Created = s.addCreatedAt
	}
	s.items = append(s.items, memory)
	return memory, nil
}

func (s *fakeMemoryStore) Remove(_ context.Context, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeCalls = append(s.removeCalls, id)
	if s.removeFails {
		return false
	}
	for index, memory := range s.items {
		if memory.ID == id {
			s.items = append(s.items[:index], s.items[index+1:]...)
			return true
		}
	}
	return false
}

func (s *fakeMemoryStore) snapshot() []contracts.Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]contracts.Memory(nil), s.items...)
}

func (s *fakeMemoryStore) searches() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.searchCalls...)
}

func (s *fakeMemoryStore) adds() []contracts.Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]contracts.Memory(nil), s.addCalls...)
}

func (s *fakeMemoryStore) removes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.removeCalls...)
}

// waitMemory pumps frames until cond holds: memory searches run off the UI
// thread and publish their result through UIRuntime.Post.
func waitMemory(t *testing.T, tester *ui.Tester, what string, cond func() bool) {
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

func memory(id, text string) contracts.Memory {
	return contracts.Memory{ID: id, Text: text, Scope: "workspace", Created: time.Unix(1700000000, 0)}
}

// --- tests ------------------------------------------------------------------

func TestMemoryBackendDisabled(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
	h := newHarness(t, chat) // no KeyMemory provided
	tester := h.tester()
	h.click(tester, "记忆")
	h.assertTexts(tester, "记忆", "记忆后端未启用")
	h.assertNoTexts(tester, "还没有记忆")
}

func TestMemoryListsAndDeletes(t *testing.T) {
	store := newFakeMemoryStore(
		memory("mem-1", "项目 Alpha 使用 Go 1.27"),
		memory("mem-2", "提交前运行 go test -race"),
	)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	h.click(tester, "记忆")

	h.assertTexts(tester, "记忆", "项目 Alpha 使用 Go 1.27", "提交前运行 go test -race",
		"工作区", "删除记忆", "新建记忆", "添加记忆")
	h.assertNoTexts(tester, "记忆后端未启用")
	// The entries are an official List: named, with rows named by their text.
	if _, ok := tester.AccessByRole(ui.RoleList, "记忆列表"); !ok {
		t.Fatalf("the memory entries are not a named list; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "项目 Alpha 使用 Go 1.27"); !ok {
		t.Fatalf("memory rows are not exposed by name; texts = %v", tester.Texts())
	}
	// H25: the 记忆 tab is the only page title; the pane starts with the
	// search field instead of a repeated big heading (tab labels appear twice:
	// accessible label + visible text, like 对话/轨迹).
	if got, want := exactTextCount(tester.Texts(), "记忆"), exactTextCount(tester.Texts(), "对话"); got != want {
		t.Fatalf("记忆 appears %d times, 对话 %d; the pane repeats its tab title: %v", got, want, tester.Texts())
	}

	// Deleting one entry asks through a native AlertDialog first.
	if err := tester.Click("删除记忆"); err != nil {
		t.Fatalf("click first delete: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "删除记忆", "取消", "删除")
	h.click(tester, "删除")
	if got := store.removes(); len(got) != 1 || got[0] != "mem-1" {
		t.Fatalf("Remove calls = %v, want [mem-1]", got)
	}
	h.assertNoTexts(tester, "项目 Alpha 使用 Go 1.27")
	h.assertTexts(tester, "提交前运行 go test -race")
}

func TestMemorySearchCallsStore(t *testing.T) {
	store := newFakeMemoryStore(
		memory("mem-1", "项目 Alpha 使用 Go 1.27"),
		memory("mem-2", "Beta 发布清单"),
	)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	h.click(tester, "记忆")
	h.assertTexts(tester, "项目 Alpha 使用 Go 1.27", "Beta 发布清单")

	// Typing does not read the store inside the frame: the debounced read starts
	// only after the user stops typing (S-17).
	h.click(tester, "搜索记忆")
	tester.Type("alpha")
	tester.Frame()
	if calls := store.searches(); len(calls) != 0 {
		t.Fatalf("search read inside the typing frame, want debounced: %v", calls)
	}
	tester.Advance(250 * time.Millisecond)
	waitMemory(t, tester, "alpha search", func() bool {
		calls := store.searches()
		return len(calls) == 1 && calls[0] == "alpha" &&
			tester.HasText("项目 Alpha 使用 Go 1.27") && !tester.HasText("Beta 发布清单")
	})

	// Clearing the query lists everything again (also through the debounce).
	h.click(tester, "搜索记忆")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	tester.Advance(250 * time.Millisecond)
	waitMemory(t, tester, "cleared list", func() bool {
		return tester.HasText("项目 Alpha 使用 Go 1.27") && tester.HasText("Beta 发布清单")
	})

	// A query with no match shows the search empty state.
	h.click(tester, "搜索记忆")
	tester.Type("zzz")
	tester.Frame()
	tester.Advance(250 * time.Millisecond)
	waitMemory(t, tester, "empty search state", func() bool {
		return tester.HasText("没有匹配的记忆")
	})
}

func TestMemoryAddRefreshes(t *testing.T) {
	store := newFakeMemoryStore()
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	h.click(tester, "记忆")
	h.assertTexts(tester, "还没有记忆")

	h.click(tester, "新建记忆")
	tester.Type("记住：发布前跑全量测试")
	tester.Frame()
	h.click(tester, "添加记忆")
	tester.Frame()

	adds := store.adds()
	if len(adds) != 1 {
		t.Fatalf("Add calls = %v, want one", adds)
	}
	if adds[0].Text != "记住：发布前跑全量测试" || adds[0].Scope != "workspace" {
		t.Fatalf("Add call = %+v, want the typed text with workspace scope", adds[0])
	}
	if len(store.snapshot()) != 1 {
		t.Fatalf("store items = %v, want the added memory", store.snapshot())
	}
	// The frame refreshed from the store (runtime.Invalidate in the click).
	h.assertTexts(tester, "记住：发布前跑全量测试", "工作区")
}

func TestMemoryErrorsAreShown(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		store := newFakeMemoryStore()
		store.listErr = errors.New("disk unavailable")
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
		h := newHarnessWithMemory(t, chat, store)
		tester := h.tester()
		h.click(tester, "记忆")
		h.assertTexts(tester, "读取记忆失败：disk unavailable")
	})

	t.Run("add", func(t *testing.T) {
		store := newFakeMemoryStore()
		store.addErr = errors.New("read-only store")
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
		h := newHarnessWithMemory(t, chat, store)
		tester := h.tester()
		h.click(tester, "记忆")
		h.click(tester, "新建记忆")
		tester.Type("一条会失败的记忆")
		tester.Frame()
		h.click(tester, "添加记忆")
		tester.Frame()
		h.assertTexts(tester, "添加记忆失败：read-only store")
		if len(store.snapshot()) != 0 {
			t.Fatalf("store items = %v, want none", store.snapshot())
		}
	})

	t.Run("remove", func(t *testing.T) {
		store := newFakeMemoryStore(memory("mem-1", "删不掉的记忆"))
		store.removeFails = true
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
		h := newHarnessWithMemory(t, chat, store)
		tester := h.tester()
		h.click(tester, "记忆")
		if err := tester.Click("删除记忆"); err != nil {
			t.Fatalf("click delete: %v", err)
		}
		tester.Frame()
		h.click(tester, "删除")
		h.assertTexts(tester, "删除记忆失败：mem-1")
		h.assertTexts(tester, "删不掉的记忆") // the entry stays listed
	})
}

// --- Markdown assistant rendering -------------------------------------------

func joinedTexts(tester *ui.Tester) string {
	return strings.Join(tester.Texts(), "\n")
}

func TestAssistantMarkdownRendering(t *testing.T) {
	markdownText := "# 修复计划\n\n- 检查认证模块\n- 补充回归测试\n\n```go\nfmt.Println(\"hi\")\n```"
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "Markdown 会话",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "# 用户原文不做 markdown"),
			{
				ID:        "m2",
				Role:      contracts.ChatRoleAssistant,
				Text:      markdownText,
				CreatedAt: time.Unix(1700000001, 0),
			},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	texts := joinedTexts(tester)
	for _, want := range []string{"修复计划", "检查认证模块", "补充回归测试", `fmt.Println("hi")`} {
		if !strings.Contains(texts, want) {
			t.Fatalf("markdown frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	// The heading markers and list bullets are rendered, not shown literally.
	if strings.Contains(texts, "# 修复计划") || strings.Contains(texts, "- 检查认证模块") {
		t.Fatalf("markdown markers leaked into the frame: %v", tester.Texts())
	}
	// User messages stay plain text (no Markdown interpretation).
	h.assertTexts(tester, "# 用户原文不做 markdown")
}

func TestStreamingAssistantMarkdownRendering(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "流式 Markdown",
		Messages: []contracts.ChatMessage{{
			ID:        "m1",
			Role:      contracts.ChatRoleAssistant,
			Text:      "正在分析 **认证模块** 的实现",
			Streaming: true,
			CreatedAt: time.Unix(1700000001, 0),
		}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	texts := joinedTexts(tester)
	for _, want := range []string{"正在分析", "认证模块", "生成中"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("streaming frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if strings.Contains(texts, "**认证模块**") {
		t.Fatalf("streaming markdown markers leaked: %v", tester.Texts())
	}
}

// TestMemoryPaneOpensAtTop covers N4: the Memory pane is not a streaming log, so
// it opens at the top with its title, search and management row visible instead
// of scrolling to the end like the transcript.
func TestMemoryPaneOpensAtTop(t *testing.T) {
	entries := make([]contracts.Memory, 0, 30)
	for index := 0; index < 30; index++ {
		entries = append(entries, memory(fmt.Sprintf("mem-%d", index),
			fmt.Sprintf("记忆内容 %d：%s", index, strings.Repeat("细节 ", 20))))
	}
	store := newFakeMemoryStore(entries...)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆顶部"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	h.click(tester, "记忆")

	for _, label := range []string{"搜索记忆", "整理记忆", "全部清空"} {
		rect, ok := tester.Find(label)
		if !ok {
			t.Fatalf("%q missing; texts = %v", label, tester.Texts())
		}
		if rect.Y < 0 || rect.Y > 780 {
			t.Fatalf("%q at y=%.1f, want it inside the window (the pane opens at the top)", label, rect.Y)
		}
	}

	// The chat transcript is the only pane that follows the streaming end.
	h.click(tester, "对话")
	tester.Frame()
	h.assertNoTexts(tester, "回到最新")
}

// TestMemorySearchDebouncesKeystrokes covers S-17's debounce: two quick query
// edits cost one background read (the last query), not one per keystroke.
func TestMemorySearchDebouncesKeystrokes(t *testing.T) {
	store := newFakeMemoryStore(
		memory("mem-1", "项目 Alpha 使用 Go 1.27"),
		memory("mem-2", "Beta 发布清单"),
	)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	h.click(tester, "记忆")
	h.click(tester, "搜索记忆")
	tester.Type("a")
	tester.Frame()
	tester.Type("lpha")
	tester.Frame()
	if calls := store.searches(); len(calls) != 0 {
		t.Fatalf("searches before the debounce window = %v, want none", calls)
	}
	tester.Advance(250 * time.Millisecond)
	waitMemory(t, tester, "single debounced search", func() bool {
		calls := store.searches()
		return len(calls) == 1 && calls[0] == "alpha"
	})
	// Nothing more is read while the result stays valid.
	tester.Advance(500 * time.Millisecond)
	for frame := 0; frame < 3; frame++ {
		tester.Frame()
	}
	if calls := store.searches(); len(calls) != 1 {
		t.Fatalf("extra searches after the debounce = %v, want one", calls)
	}
}

// TestMemoryAddEnterSubmits covers S-17's keyboard path: Enter in the add input
// submits the memory (empty draft keeps the action disabled and Enter a no-op).
func TestMemoryAddEnterSubmits(t *testing.T) {
	store := newFakeMemoryStore()
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	h.click(tester, "记忆")
	if info, ok := tester.Access("添加记忆"); !ok || !info.Disabled {
		t.Fatalf("add action with an empty draft = %+v found %v, want disabled", info, ok)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if adds := store.adds(); len(adds) != 0 {
		t.Fatalf("Enter with an empty draft added %v", adds)
	}

	h.click(tester, "新建记忆")
	tester.Type("记住：发布前跑全量测试")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	adds := store.adds()
	if len(adds) != 1 || adds[0].Text != "记住：发布前跑全量测试" || adds[0].Scope != "workspace" {
		t.Fatalf("Enter add calls = %+v, want one workspace memory", adds)
	}
	if info, ok := tester.Access("新建记忆"); !ok || info.Value != "" {
		t.Fatalf("add input after Enter = %+v found %v, want an empty draft", info, ok)
	}
	// The refresh renders the new entry through the cached read model.
	waitMemory(t, tester, "added memory row", func() bool {
		return tester.HasText("记住：发布前跑全量测试")
	})
}
