package settings_test

// TDD tests (RED first) for the settings Memory page (global memory management).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeMemoryStore is a scriptable contracts.MemoryStore for the page tests.
type fakeMemoryStore struct {
	mu sync.Mutex

	items []contracts.Memory
	calls struct {
		list   int
		search []string
		added  []contracts.Memory
		remove []string
	}
	listErr   error
	searchErr error
	addErr    error
	removeErr bool
}

func newFakeMemoryStore(items ...contracts.Memory) *fakeMemoryStore {
	return &fakeMemoryStore{items: append([]contracts.Memory(nil), items...)}
}

func (m *fakeMemoryStore) List(context.Context) ([]contracts.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls.list++
	if m.listErr != nil {
		return nil, m.listErr
	}
	return append([]contracts.Memory(nil), m.items...), nil
}

func (m *fakeMemoryStore) Search(_ context.Context, query string) ([]contracts.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls.search = append(m.calls.search, query)
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	found := make([]contracts.Memory, 0, len(m.items))
	for _, memory := range m.items {
		if strings.Contains(strings.ToLower(memory.Text), strings.ToLower(query)) {
			found = append(found, memory)
		}
	}
	return found, nil
}

func (m *fakeMemoryStore) Add(_ context.Context, memory contracts.Memory) (contracts.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls.added = append(m.calls.added, memory)
	if m.addErr != nil {
		return contracts.Memory{}, m.addErr
	}
	memory.ID = "mem-added"
	memory.Created = time.Unix(1700000000, 0)
	m.items = append(m.items, memory)
	return memory, nil
}

func (m *fakeMemoryStore) Remove(_ context.Context, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls.remove = append(m.calls.remove, id)
	if m.removeErr {
		return false
	}
	for index, memory := range m.items {
		if memory.ID == id {
			m.items = append(m.items[:index], m.items[index+1:]...)
			return true
		}
	}
	return false
}

func (m *fakeMemoryStore) snapshot() []contracts.Memory {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]contracts.Memory(nil), m.items...)
}

func (m *fakeMemoryStore) added() []contracts.Memory {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]contracts.Memory(nil), m.calls.added...)
}

func (m *fakeMemoryStore) searches() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls.search...)
}

func (m *fakeMemoryStore) removes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls.remove...)
}

func memoryEntry(id, text string) contracts.Memory {
	return contracts.Memory{ID: id, Text: text, Scope: "global", Created: time.Unix(1700000000, 0)}
}

// memoryPageHarness opens the data page with an optional memory backend
// provided after activation (the page resolves it per frame).
func memoryPageHarness(t *testing.T, store contracts.MemoryStore) *harness {
	t.Helper()
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	if store != nil {
		provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, store)
	}
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	return h
}

// --- tests ------------------------------------------------------------------

func TestMemoryPageEmptyState(t *testing.T) {
	store := newFakeMemoryStore()
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.assertTexts(tester, "记忆", "还没有记忆", "0 条记忆", "添加", "记忆内容")
}

func TestMemoryPageRendersEntries(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "项目使用 Go 1.27"),
		memoryEntry("mem-2", "提交前运行 go test -race"),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.assertTexts(tester, "2 条记忆", "项目使用 Go 1.27", "提交前运行 go test -race",
		"全局", "删除", "搜索记忆")
}

func TestMemoryPageSearchUsesStore(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "Alpha 计划"),
		memoryEntry("mem-2", "Beta 计划"),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()

	h.focusInput(tester, "搜索记忆")
	tester.Type("Alpha")
	tester.Frame()
	if calls := store.searches(); len(calls) == 0 || calls[len(calls)-1] != "Alpha" {
		t.Fatalf("Search calls = %v, want Alpha", calls)
	}
	h.assertTexts(tester, "Alpha 计划")
	h.assertNoTexts(tester, "Beta 计划")

	h.focusInput(tester, "搜索记忆")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.assertTexts(tester, "Alpha 计划", "Beta 计划")
}

func TestMemoryPageAdd(t *testing.T) {
	store := newFakeMemoryStore()
	h := memoryPageHarness(t, store)
	tester := h.tester()

	h.focusInput(tester, "记忆内容 输入")
	tester.Type("记住这次结论")
	tester.Frame()
	h.click(tester, "添加")

	added := store.added()
	if len(added) != 1 || added[0].Text != "记住这次结论" || added[0].Scope != "global" {
		t.Fatalf("Add calls = %+v, want global/记住这次结论", added)
	}
	h.assertTexts(tester, "已添加", "1 条记忆", "记住这次结论")
}

func TestMemoryPageAddDisabledWhenEmpty(t *testing.T) {
	store := newFakeMemoryStore()
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.click(tester, "添加")
	if added := store.added(); len(added) != 0 {
		t.Fatalf("empty add reached the store: %+v", added)
	}
}

func TestMemoryPageDelete(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "要删除的记忆"),
		memoryEntry("mem-2", "保留的记忆"),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.click(tester, "删除")
	if removes := store.removes(); len(removes) != 1 || removes[0] != "mem-1" {
		t.Fatalf("Remove calls = %v, want [mem-1]", removes)
	}
	h.assertTexts(tester, "1 条记忆", "保留的记忆")
	h.assertNoTexts(tester, "要删除的记忆")
}

func TestMemoryPageErrors(t *testing.T) {
	store := newFakeMemoryStore()
	store.listErr = errors.New("list boom")
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.assertTexts(tester, "读取记忆失败：list boom")

	addStore := newFakeMemoryStore()
	addStore.addErr = errors.New("add boom")
	h2 := memoryPageHarness(t, addStore)
	tester2 := h2.tester()
	h2.focusInput(tester2, "记忆内容 输入")
	tester2.Type("失败")
	tester2.Frame()
	h2.click(tester2, "添加")
	h2.assertTexts(tester2, "添加记忆失败：add boom")

	removeStore := newFakeMemoryStore(memoryEntry("mem-1", "删不掉"))
	removeStore.removeErr = true
	h3 := memoryPageHarness(t, removeStore)
	tester3 := h3.tester()
	h3.click(tester3, "删除")
	h3.assertTexts(tester3, "删除记忆失败：mem-1")
}

func TestMemoryPageWithoutBackend(t *testing.T) {
	h := memoryPageHarness(t, nil)
	tester := h.tester()
	h.assertTexts(tester, "未启用记忆后端")
	h.assertNoTexts(tester, "搜索记忆", "添加", "还没有记忆")
}

func TestMemoryPageEnglishCopy(t *testing.T) {
	store := newFakeMemoryStore(memoryEntry("mem-1", "Alpha note"))
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, store)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Memory")
	h.assertTexts(tester, "Memory", "1 memory", "Alpha note", "Search memory", "Add", "Delete")

	h.focusInput(tester, "Memory text input")
	tester.Type("remember this")
	tester.Frame()
	h.click(tester, "Add")
	h.assertTexts(tester, "Added", "2 memories")
}

// --- management: tidy / clear all -------------------------------------------

func memoryAt(id, text string, created time.Time) contracts.Memory {
	return contracts.Memory{ID: id, Text: text, Scope: "global", Created: created}
}

func TestMemoryPageTidyRemovesDuplicatesKeepsEarliest(t *testing.T) {
	base := time.Unix(1700000000, 0)
	store := newFakeMemoryStore(
		memoryAt("mem-1", "Alpha 项目", base),
		memoryAt("mem-2", "alpha   项目", base.Add(time.Second)),
		memoryAt("mem-3", "Beta 计划", base.Add(2*time.Second)),
		memoryAt("mem-4", " ALPHA 项目 ", base.Add(3*time.Second)),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()

	h.click(tester, "整理记忆")
	h.assertTexts(tester, "已整理 2 条")
	if removes := store.removes(); len(removes) != 2 || removes[0] != "mem-2" || removes[1] != "mem-4" {
		t.Fatalf("Remove calls = %v, want [mem-2 mem-4]", removes)
	}
	remaining := store.snapshot()
	if len(remaining) != 2 || remaining[0].ID != "mem-1" || remaining[1].ID != "mem-3" {
		t.Fatalf("remaining memories = %+v", remaining)
	}
}

func TestMemoryPageTidyWithoutDuplicates(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "Alpha"),
		memoryEntry("mem-2", "Beta"),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.click(tester, "整理记忆")
	h.assertTexts(tester, "没有可整理的重复记忆")
	if removes := store.removes(); len(removes) != 0 {
		t.Fatalf("Remove calls = %v, want none", removes)
	}
}

func TestMemoryPageTidyErrors(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "Alpha"),
		memoryEntry("mem-2", "alpha"),
	)
	store.removeErr = true
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.click(tester, "整理记忆")
	h.assertTexts(tester, "整理记忆失败")

	listStore := newFakeMemoryStore()
	listStore.listErr = errors.New("tidy list boom")
	h2 := memoryPageHarness(t, listStore)
	tester2 := h2.tester()
	h2.click(tester2, "整理记忆")
	h2.assertTexts(tester2, "读取记忆失败：tidy list boom")
}

func TestMemoryPageClearAllConfirmed(t *testing.T) {
	store := newFakeMemoryStore(
		memoryEntry("mem-1", "Alpha"),
		memoryEntry("mem-2", "Beta"),
		memoryEntry("mem-3", "Gamma"),
	)
	h := memoryPageHarness(t, store)
	tester := h.tester()

	h.click(tester, "全部清空")
	h.assertTexts(tester, "清空全部记忆", "取消", "清空")
	h.click(tester, "清空")
	h.assertTexts(tester, "已清空")
	if remaining := store.snapshot(); len(remaining) != 0 {
		t.Fatalf("memories after clear = %+v, want none", remaining)
	}
	if removes := store.removes(); len(removes) != 3 {
		t.Fatalf("Remove calls = %v, want all three", removes)
	}
}

func TestMemoryPageClearAllCancelled(t *testing.T) {
	store := newFakeMemoryStore(memoryEntry("mem-1", "Alpha"))
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.click(tester, "全部清空")
	h.click(tester, "取消")
	if removes := store.removes(); len(removes) != 0 {
		t.Fatalf("cancelled clear called Remove: %v", removes)
	}
	if remaining := store.snapshot(); len(remaining) != 1 {
		t.Fatalf("memories after cancel = %+v", remaining)
	}
}

func TestMemoryPageClearAllErrors(t *testing.T) {
	store := newFakeMemoryStore(memoryEntry("mem-1", "Alpha"))
	store.removeErr = true
	h := memoryPageHarness(t, store)
	tester := h.tester()
	h.click(tester, "全部清空")
	h.click(tester, "清空")
	h.assertTexts(tester, "清空失败")
}

func TestMemoryPageAdminEnglishCopy(t *testing.T) {
	store := newFakeMemoryStore(
		memoryAt("mem-1", "Alpha", time.Unix(1700000000, 0)),
		memoryAt("mem-2", "alpha", time.Unix(1700000001, 0)),
	)
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, store)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Memory")
	h.assertTexts(tester, "Tidy", "Clear all")
	h.click(tester, "Tidy")
	h.assertTexts(tester, "Tidied 1 memory")
	h.click(tester, "Clear all")
	h.assertTexts(tester, "Clear all memories", "Cancel", "Clear")
	h.click(tester, "Clear")
	h.assertTexts(tester, "Cleared")
}

// --- model extraction (opt-in) ----------------------------------------------

// extractCall is one recorded ExtractFromSession invocation.
type extractCall struct {
	sessionID string
	max       int
}

// fakeMemoryExtractor is a scriptable contracts.MemoryExtractionService.
type fakeMemoryExtractor struct {
	mu      sync.Mutex
	calls   []extractCall
	result  []contracts.Memory
	err     error
	block   chan struct{}
	publish func([]contracts.Memory)
}

func (f *fakeMemoryExtractor) ExtractFromSession(ctx context.Context, sessionID string, max int) ([]contracts.Memory, error) {
	f.mu.Lock()
	f.calls = append(f.calls, extractCall{sessionID: sessionID, max: max})
	block := f.block
	result := append([]contracts.Memory(nil), f.result...)
	err := f.err
	publish := f.publish
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if publish != nil {
		publish(result)
	}
	return result, nil
}

func (f *fakeMemoryExtractor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeMemoryExtractor) lastCall() extractCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return extractCall{}
	}
	return f.calls[len(f.calls)-1]
}

// memoryExtractHarness opens the Memory page with an optional extraction
// backend and memory store.
func memoryExtractHarness(t *testing.T, chat *fakeChat, store *fakeSettings,
	memory contracts.MemoryStore, extractor contracts.MemoryExtractionService) (*harness, *ui.Tester) {
	t.Helper()
	h := newHarness(t, chat, store, settings.Config{})
	if memory != nil {
		provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, memory)
	}
	if extractor != nil {
		provide[contracts.MemoryExtractionService](t, h.ctx, contracts.KeyMemoryExtractor, extractor)
	}
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	return h, tester
}

// waitMemoryText frames until the text appears (extraction runs async).
func waitMemoryText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tester.Frame()
		if tester.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("memory page did not show %q; texts = %v", want, tester.Texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func memoryExtractChat() *fakeChat {
	chat := &fakeChat{}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆会话"})
	chat.mu.Lock()
	chat.active = "sess-1"
	chat.mu.Unlock()
	return chat
}

func memoryExtractSettings(raw string) *fakeSettings {
	if raw == "" {
		return newFakeSettings(nil)
	}
	return newFakeSettings(map[string]string{contracts.SettingMemoryExtractEnabled: raw})
}

func TestMemoryExtractToggleDefaultOff(t *testing.T) {
	store := newFakeMemoryStore()
	settingsStore := memoryExtractSettings("")
	extractor := &fakeMemoryExtractor{}
	h, tester := memoryExtractHarness(t, memoryExtractChat(), settingsStore, store, extractor)
	h.assertTexts(tester, "模型提取", "启用模型提取（从最近对话提取记忆，会调用模型）",
		"最近 20 条消息", "最多写入 5 条记忆", "从当前会话提取")
	if raw := settingsStore.get(contracts.SettingMemoryExtractEnabled); raw != "" {
		t.Fatalf("defaults wrote memory.extract: %q", raw)
	}
	// The extract action is disabled while the opt-in is off.
	h.click(tester, "从当前会话提取")
	if extractor.callCount() != 0 {
		t.Fatalf("extraction ran while disabled: %v", extractor.calls)
	}

	h.click(tester, "启用模型提取（从最近对话提取记忆，会调用模型）")
	if raw := settingsStore.get(contracts.SettingMemoryExtractEnabled); raw != "true" {
		t.Fatalf("enabling wrote %q, want true", raw)
	}
	h.click(tester, "启用模型提取（从最近对话提取记忆，会调用模型）")
	if raw := settingsStore.get(contracts.SettingMemoryExtractEnabled); raw != "" {
		t.Fatalf("disabling wrote %q, want empty", raw)
	}
}

func TestMemoryExtractBadValueShowsOff(t *testing.T) {
	store := newFakeMemoryStore()
	settingsStore := memoryExtractSettings("yes")
	extractor := &fakeMemoryExtractor{}
	h, tester := memoryExtractHarness(t, memoryExtractChat(), settingsStore, store, extractor)
	_ = tester
	// The malformed value displays as off, so one click enables it.
	h.click(tester, "启用模型提取（从最近对话提取记忆，会调用模型）")
	if raw := settingsStore.get(contracts.SettingMemoryExtractEnabled); raw != "true" {
		t.Fatalf("toggle from a malformed value wrote %q, want true", raw)
	}
}

func TestMemoryExtractHiddenWithoutExtractor(t *testing.T) {
	store := newFakeMemoryStore()
	h, tester := memoryExtractHarness(t, memoryExtractChat(), memoryExtractSettings("true"), store, nil)
	h.assertTexts(tester, "未启用记忆提取后端")
	if tester.HasText("从当前会话提取") {
		t.Fatalf("extract action rendered without a backend; texts = %v", tester.Texts())
	}
}

func TestMemoryExtractSuccessAddsMemories(t *testing.T) {
	store := newFakeMemoryStore()
	extractor := &fakeMemoryExtractor{
		result: []contracts.Memory{
			{ID: "new-1", Text: "偏好使用 Go 1.27", Scope: "global"},
			{ID: "new-2", Text: "提交前运行 race 测试", Scope: "global"},
		},
		publish: func(memories []contracts.Memory) {
			for _, memory := range memories {
				_, _ = store.Add(context.Background(), memory)
			}
		},
	}
	h, tester := memoryExtractHarness(t, memoryExtractChat(), memoryExtractSettings("true"), store, extractor)
	h.click(tester, "从当前会话提取")
	waitMemoryText(t, tester, "已添加 2 条记忆")
	call := extractor.lastCall()
	if call.sessionID != "sess-1" || call.max != 5 {
		t.Fatalf("Extract call = %+v, want sess-1/5", call)
	}
	h.assertTexts(tester, "偏好使用 Go 1.27", "提交前运行 race 测试")
}

func TestMemoryExtractFailureDanger(t *testing.T) {
	store := newFakeMemoryStore()
	extractor := &fakeMemoryExtractor{err: errors.New("provider boom")}
	h, tester := memoryExtractHarness(t, memoryExtractChat(), memoryExtractSettings("true"), store, extractor)
	h.click(tester, "从当前会话提取")
	waitMemoryText(t, tester, "提取失败：provider boom")
}

func TestMemoryExtractIgnoresRepeatedClicks(t *testing.T) {
	store := newFakeMemoryStore()
	extractor := &fakeMemoryExtractor{block: make(chan struct{}), result: []contracts.Memory{}}
	h, tester := memoryExtractHarness(t, memoryExtractChat(), memoryExtractSettings("true"), store, extractor)
	h.click(tester, "从当前会话提取")
	h.assertTexts(tester, "提取中…")
	h.click(tester, "提取中…")
	tester.Frame()
	if got := extractor.callCount(); got != 1 {
		t.Fatalf("Extract calls = %d, want 1 while running", got)
	}
	close(extractor.block)
	waitMemoryText(t, tester, "已添加 0 条记忆")
}

func TestMemoryExtractDisabledWithoutActiveSession(t *testing.T) {
	store := newFakeMemoryStore()
	extractor := &fakeMemoryExtractor{}
	chat := &fakeChat{} // no active session
	h, tester := memoryExtractHarness(t, chat, memoryExtractSettings("true"), store, extractor)
	h.assertTexts(tester, "当前没有活动会话")
	h.click(tester, "从当前会话提取")
	if extractor.callCount() != 0 {
		t.Fatalf("extraction ran without an active session: %v", extractor.calls)
	}
}

func TestMemoryExtractEnglishCopy(t *testing.T) {
	store := newFakeMemoryStore()
	extractor := &fakeMemoryExtractor{result: []contracts.Memory{
		{ID: "new-1", Text: "prefers Go", Scope: "global"},
		{ID: "new-2", Text: "runs race tests", Scope: "global"},
	}}
	h := newLocaleHarness(t, memoryExtractChat(), memoryExtractSettings("true"), settings.Config{}, "en")
	provide[contracts.MemoryStore](t, h.ctx, contracts.KeyMemory, store)
	provide[contracts.MemoryExtractionService](t, h.ctx, contracts.KeyMemoryExtractor, extractor)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Memory")
	h.assertTexts(tester, "Model extraction", "Enable model extraction",
		"Extract from current session", "recent 20 messages", "at most 5")
	h.click(tester, "Extract from current session")
	waitMemoryText(t, tester, "Added 2 memories")
}
