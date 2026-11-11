package session_test

// TDD tests (RED first) for the Memory tab's management actions: tidy
// duplicates (keep the earliest) and clear-all with a native confirmation.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func memoryAt(id, text string, at time.Time) contracts.Memory {
	return contracts.Memory{ID: id, Text: text, Scope: "workspace", Created: at}
}

func tidyChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-memory", Title: "记忆会话"}, true)
	return chat
}

func openMemoryTab(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "记忆")
}

// --- tidy -------------------------------------------------------------------

func TestMemoryTidyRemovesDuplicatesKeepsEarliest(t *testing.T) {
	base := time.Unix(1700000000, 0)
	store := newFakeMemoryStore(
		memoryAt("mem-1", "Alpha 项目", base),
		memoryAt("mem-2", "alpha   项目", base.Add(time.Second)),
		memoryAt("mem-3", "Beta 计划", base.Add(2*time.Second)),
		memoryAt("mem-4", " ALPHA 项目 ", base.Add(3*time.Second)),
	)
	notifier := &fakeSessionNotifier{}
	h := newMemoryNotifierHarness(t, tidyChat(), store, notifier)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "整理记忆")
	h.assertTexts(tester, "整理记忆", "取消", "整理")
	h.click(tester, "整理")
	if !memoryToastContains(notifier, "已整理 2 条") {
		t.Fatalf("tidy toast missing; toasts = %+v", notifier.toasts())
	}
	removes := store.removes()
	if len(removes) != 2 || removes[0] != "mem-2" || removes[1] != "mem-4" {
		t.Fatalf("Remove calls = %v, want [mem-2 mem-4]", removes)
	}
	remaining := store.snapshot()
	ids := make([]string, 0, len(remaining))
	for _, memory := range remaining {
		ids = append(ids, memory.ID)
	}
	if len(ids) != 2 || ids[0] != "mem-1" || ids[1] != "mem-3" {
		t.Fatalf("remaining memories = %v, want [mem-1 mem-3]", ids)
	}
}

func TestMemoryTidyWithoutDuplicates(t *testing.T) {
	store := newFakeMemoryStore(
		memory("mem-1", "Alpha"),
		memory("mem-2", "Beta"),
	)
	notifier := &fakeSessionNotifier{}
	h := newMemoryNotifierHarness(t, tidyChat(), store, notifier)
	tester := h.tester()
	openMemoryTab(t, h, tester)
	h.click(tester, "整理记忆")
	h.click(tester, "整理")
	if !memoryToastContains(notifier, "没有可整理的重复记忆") {
		t.Fatalf("tidy info toast missing; toasts = %+v", notifier.toasts())
	}
	if removes := store.removes(); len(removes) != 0 {
		t.Fatalf("Remove calls = %v, want none", removes)
	}
}

func TestMemoryTidyErrors(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha"))
	store.listErr = errors.New("memory store down")
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)
	h.click(tester, "整理记忆")
	h.click(tester, "整理")
	h.assertTexts(tester, "读取记忆失败：memory store down")

	store2 := newFakeMemoryStore(
		memory("mem-1", "Alpha"),
		memory("mem-2", "alpha"),
	)
	store2.removeFails = true
	h2 := newHarnessWithMemory(t, tidyChat(), store2)
	tester2 := h2.tester()
	openMemoryTab(t, h2, tester2)
	h2.click(tester2, "整理记忆")
	h2.click(tester2, "整理")
	// The failure says how many entries could not be removed and that the user
	// can retry, instead of a bare count ("整理记忆失败：1").
	h2.assertTexts(tester2, "整理记忆失败", "1 条未能删除，请重试")
}

// --- clear all --------------------------------------------------------------

func TestMemoryClearAllConfirmed(t *testing.T) {
	store := newFakeMemoryStore(
		memory("mem-1", "Alpha"),
		memory("mem-2", "Beta"),
		memory("mem-3", "Gamma"),
	)
	notifier := &fakeSessionNotifier{}
	h := newMemoryNotifierHarness(t, tidyChat(), store, notifier)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "全部清空")
	h.assertTexts(tester, "清空记忆", "取消", "清空")
	h.click(tester, "清空")
	if !memoryToastContains(notifier, "已清空") {
		t.Fatalf("clear toast missing; toasts = %+v", notifier.toasts())
	}
	if remaining := store.snapshot(); len(remaining) != 0 {
		t.Fatalf("memories after clear = %+v, want none", remaining)
	}
	if removes := store.removes(); len(removes) != 3 {
		t.Fatalf("Remove calls = %v, want all three ids", removes)
	}
}

func TestMemoryClearAllCancelled(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha"))
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "全部清空")
	h.click(tester, "取消")
	if removes := store.removes(); len(removes) != 0 {
		t.Fatalf("cancelled clear called Remove: %v", removes)
	}
	if remaining := store.snapshot(); len(remaining) != 1 {
		t.Fatalf("memories after cancel = %+v, want the original", remaining)
	}
}

func TestMemoryClearAllError(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha"))
	store.removeFails = true
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "全部清空")
	h.click(tester, "清空")
	h.assertTexts(tester, "清空失败", "1 条记忆未能删除，请重试")
}

// TestMemoryClearDialogDoesNotRequestFrames is the S-02 regression: while the
// native clear-all AlertDialog is open (mygo returns -1), the frame builder must
// not request another frame, or the window redraws every frame for as long as
// the confirmation stays open.
func TestMemoryClearDialogDoesNotRequestFrames(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha"))
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "全部清空")
	h.assertTexts(tester, "清空记忆", "取消", "清空")

	requests := 0
	h.ui.SetInvalidator(func() { requests++ })
	for frame := 0; frame < 3; frame++ {
		tester.Frame()
	}
	if requests != 0 {
		t.Fatalf("open clear dialog requested %d frames over 3 explicit frames, want 0", requests)
	}
}

// --- statistics / hidden / i18n ---------------------------------------------

func TestMemoryStatsLine(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha"), memory("mem-2", "Beta"))
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)
	h.assertTexts(tester, "2 条记忆")
}

func TestMemoryToolsHiddenWithoutStore(t *testing.T) {
	h := newHarness(t, tidyChat())
	tester := h.tester()
	openMemoryTab(t, h, tester)
	if got := exactTextCount(tester.Texts(), "整理记忆"); got != 0 {
		t.Fatalf("tidy action rendered without a memory store; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "全部清空", "条记忆")
}

func TestMemoryToolsEnglishCopy(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha"), memory("mem-2", "Beta"))
	notifier := &fakeSessionNotifier{}
	h := newMemoryNotifierHarness(t, tidyChat(), store, notifier)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "Memory")

	h.assertTexts(tester, "2 memories", "Tidy memories", "Clear all")
	h.click(tester, "Tidy memories")
	h.click(tester, "Tidy")
	if !memoryToastContains(notifier, "No duplicate memories to tidy") {
		t.Fatalf("English tidy toast missing; toasts = %+v", notifier.toasts())
	}
}

// newMemoryNotifierHarness builds the session screen with the memory backend
// and the host notifier: the management actions report their outcome as
// transient toasts instead of resident badges.
func newMemoryNotifierHarness(t *testing.T, chat *fakeChat, memory contracts.MemoryStore, notifier contracts.Notifier) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.MemoryStore](t, ctx, contracts.KeyMemory, memory)
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// memoryToastContains reports whether the notifier recorded a toast containing
// fragment.
func memoryToastContains(notifier *fakeSessionNotifier, fragment string) bool {
	for _, toast := range notifier.toasts() {
		if strings.Contains(toast.text, fragment) {
			return true
		}
	}
	return false
}

// updateMemory is the optional contracts.MemoryUpdater half of the fake store:
// the Memory pane shows its edit action only when the backend can update.
func (s *fakeMemoryStore) Update(_ context.Context, id, text, _ string) (contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text = strings.TrimSpace(text)
	if text == "" {
		return contracts.Memory{}, errors.New("empty memory")
	}
	for index := range s.items {
		if s.items[index].ID == id {
			s.items[index].Text = text
			return s.items[index], nil
		}
	}
	return contracts.Memory{}, errors.New("unknown memory")
}

// TestMemoryPaneEditUsesPencilIcon covers the icon migration: the edit action
// is a real SVG pencil from internal/icons (no Unicode ✎ rendered as text), with
// its label/tooltip intact, and it opens the native edit dialog.
func TestMemoryPaneEditUsesPencilIcon(t *testing.T) {
	store := newFakeMemoryStore(memory("mem-1", "Alpha 项目"))
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)
	h.assertTexts(tester, "Alpha 项目")
	if got := textsContaining(tester.Texts(), "✎"); got != 0 {
		t.Fatalf("Unicode pencil glyph still rendered (%d); texts = %v", got, tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "编辑记忆"); !ok {
		t.Fatalf("edit action is not a labeled button; texts = %v", tester.Texts())
	}
	h.click(tester, "编辑记忆")
	h.assertTexts(tester, "编辑记忆", "记忆内容", "保存", "取消")
}

// --- M-11: refresh the open Memory tab when the store changes out of band ----

// versionedMemoryStore is a contracts.MemoryStore with the optional
// MemoryVersioner half: the product store advances the counter on every
// successful mutation so the visible tab can notice model-written entries.
type versionedMemoryStore struct {
	mu        sync.Mutex
	items     []contracts.Memory
	version   uint64
	listCount int
}

func (s *versionedMemoryStore) List(context.Context) ([]contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCount++
	return append([]contracts.Memory(nil), s.items...), nil
}

func (s *versionedMemoryStore) Search(_ context.Context, query string) ([]contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := make([]contracts.Memory, 0, len(s.items))
	for _, memory := range s.items {
		if strings.Contains(strings.ToLower(memory.Text), strings.ToLower(strings.TrimSpace(query))) {
			found = append(found, memory)
		}
	}
	return found, nil
}

func (s *versionedMemoryStore) Add(_ context.Context, memory contracts.Memory) (contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if memory.ID == "" {
		memory.ID = "mem-external"
	}
	s.items = append(s.items, memory)
	s.version++
	return memory, nil
}

func (s *versionedMemoryStore) Remove(_ context.Context, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, memory := range s.items {
		if memory.ID == id {
			s.items = append(s.items[:index], s.items[index+1:]...)
			s.version++
			return true
		}
	}
	return false
}

func (s *versionedMemoryStore) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

func TestMemoryTabRefreshesWhenStoreVersionAdvances(t *testing.T) {
	store := &versionedMemoryStore{items: []contracts.Memory{
		memoryAt("mem-1", "first memory", time.Unix(1700000000, 0)),
	}}
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)
	h.assertTexts(tester, "first memory")

	// The model's memory tool (or another surface) writes while the tab is
	// visible: the next frame must notice the version change and re-read.
	if _, err := store.Add(context.Background(), contracts.Memory{ID: "mem-2", Text: "added by the agent tool", Scope: "workspace"}); err != nil {
		t.Fatalf("external Add: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "added by the agent tool")
	if got := store.listCalls(); got < 2 {
		t.Fatalf("memory List calls = %d, want a re-read after the version change", got)
	}
}

// listCalls is the number of List calls seen by the versioned store (used to
// prove the refresh actually re-read instead of rendering a cached list).
func (s *versionedMemoryStore) listCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCount
}
