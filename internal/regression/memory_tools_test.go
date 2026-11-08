package regression

// Adversarial audit: the Memory tab's management actions (tidy duplicates and
// clear all) that landed in the working tree during this round. Negative/boundary
// cases: earliest duplicate wins, whitespace/case normalization, no-op tidy,
// clear confirmation cancel/confirm, empty store, persisted file, and list/remove
// failures surfacing in the danger line instead of panicking.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/memory"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// memoryHarness is the session screen on the Memory tab.
type memoryHarness struct {
	h        *harness
	store    contracts.MemoryStore
	tester   *ui.Tester
	notifier *fakeNotifier
}

// seedMemoryFile writes one memory registry document below the data directory.
func seedMemoryFile(t *testing.T, dataDir string, entries []contracts.Memory) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		t.Fatalf("encode memory seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, memory.DefaultFileName), body, 0o600); err != nil {
		t.Fatalf("write memory seed: %v", err)
	}
}

// startMemoryUI boots the session screen over the real memory backend (or the
// supplied fault-injecting store) and switches to the Memory tab.
func startMemoryUI(t *testing.T, seed []contracts.Memory, provided contracts.MemoryStore) *memoryHarness {
	t.Helper()
	h := newBareHarness(t)
	if seed != nil {
		seedMemoryFile(t, h.dataDir, seed)
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-memory", Title: "Memory audit",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"}}}, true)
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	if provided != nil {
		provide(t, h.ctx, contracts.KeyMemory, provided)
	} else {
		activate(t, h.ctx, memory.Module(), memory.Config{})
	}
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &memoryHarness{h: h, store: requireService(t, h.ctx, contracts.KeyMemory),
		tester: h.tester(1280, 800), notifier: notifier}
	harness.tester.Frame()
	if err := harness.tester.Click("记忆"); err != nil {
		t.Fatalf("open Memory tab: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	return harness
}

// click clicks one label and repaints.
func (m *memoryHarness) click(t *testing.T, label string) {
	t.Helper()
	if err := m.tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, m.tester.Texts())
	}
	m.tester.Frame()
}

// entries reads the store contents.
func (m *memoryHarness) entries(t *testing.T) []contracts.Memory {
	t.Helper()
	entries, err := m.store.List(nil)
	if err != nil {
		t.Fatalf("List memories: %v", err)
	}
	return entries
}

// texts returns the memory bodies currently stored.
func (m *memoryHarness) texts(t *testing.T) []string {
	t.Helper()
	out := make([]string, 0)
	for _, entry := range m.entries(t) {
		out = append(out, entry.Text)
	}
	return out
}

// seed builds one memory entry with a deterministic creation time.
func memorySeed(index int, text string) contracts.Memory {
	return contracts.Memory{ID: "mem-seed-" + itoaSmall(index), Text: text,
		Scope: "global", Created: time.Unix(1_700_000_000+int64(index), 0)}
}

// itoaSmall renders one small non-negative integer without importing strconv.
func itoaSmall(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// TestMemoryTidyKeepsEarliestDuplicate covers the tidy action: duplicates are
// compared after trimming/collapsing whitespace and lowercasing, the earliest
// copy survives and the removed count is reported.
func TestMemoryTidyKeepsEarliestDuplicate(t *testing.T) {
	seed := []contracts.Memory{
		memorySeed(0, "Buy milk"),
		memorySeed(1, "buy  milk"),
		memorySeed(2, " BUY MILK "),
		memorySeed(3, "Unique note"),
		memorySeed(4, "另一条"),
	}
	harness := startMemoryUI(t, seed, nil)
	requireHasText(t, harness.tester, "5 条记忆")

	harness.click(t, "整理记忆")
	harness.click(t, "整理") // the native confirmation
	requireToast(t, harness.notifier, "已整理 2 条")
	requireHasText(t, harness.tester, "3 条记忆")

	texts := harness.texts(t)
	if len(texts) != 3 {
		t.Fatalf("stored memories = %v, want 3", texts)
	}
	want := map[string]bool{"Buy milk": true, "Unique note": true, "另一条": true}
	for _, text := range texts {
		if !want[text] {
			t.Fatalf("unexpected surviving memory %q; want the earliest of each duplicate", text)
		}
	}
	// The earliest entry's ID survives.
	foundEarliest := false
	for _, entry := range harness.entries(t) {
		if entry.ID == "mem-seed-0" {
			foundEarliest = true
		}
	}
	if !foundEarliest {
		t.Fatal("tidy removed the earliest duplicate instead of a later copy")
	}
}

// TestMemoryTidyWithoutDuplicatesIsNoop covers a clean store: tidy reports that
// there is nothing to tidy and leaves every entry in place.
func TestMemoryTidyWithoutDuplicatesIsNoop(t *testing.T) {
	harness := startMemoryUI(t, []contracts.Memory{memorySeed(0, "alpha"), memorySeed(1, "beta")}, nil)
	harness.click(t, "整理记忆")
	harness.click(t, "整理")
	requireToast(t, harness.notifier, "没有可整理的重复记忆")
	if got := len(harness.entries(t)); got != 2 {
		t.Fatalf("memories after no-op tidy = %d, want 2", got)
	}
}

// TestMemoryClearAllConfirmCancel covers the clear-all confirmation: cancel keeps
// the entries, confirm removes them and reports the empty state.
func TestMemoryClearAllConfirmCancel(t *testing.T) {
	harness := startMemoryUI(t, []contracts.Memory{
		memorySeed(0, "one"), memorySeed(1, "two"), memorySeed(2, "three"),
	}, nil)

	harness.click(t, "全部清空")
	requireHasText(t, harness.tester, "清空记忆")
	requireHasText(t, harness.tester, "确认删除全部记忆吗？此操作无法撤销。")
	harness.click(t, "取消")
	if got := len(harness.entries(t)); got != 3 {
		t.Fatalf("memories after cancel = %d, want 3", got)
	}

	harness.click(t, "全部清空")
	harness.click(t, "清空")
	if got := len(harness.entries(t)); got != 0 {
		t.Fatalf("memories after clear = %d, want 0", got)
	}
	requireToast(t, harness.notifier, "已清空")
	requireHasText(t, harness.tester, "0 条记忆")
	requireHasText(t, harness.tester, "还没有记忆")

	// The empty registry was persisted.
	body, err := os.ReadFile(filepath.Join(harness.h.dataDir, memory.DefaultFileName))
	if err != nil {
		t.Fatalf("read persisted memory file: %v", err)
	}
	var document struct {
		Entries []contracts.Memory `json:"entries"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode persisted memory file: %v", err)
	}
	if len(document.Entries) != 0 {
		t.Fatalf("persisted entries = %+v, want none", document.Entries)
	}
}

// TestMemoryClearAllEmptyStore covers clearing an empty store: it is a safe
// success (no error line) and does not panic on a missing file.
func TestMemoryClearAllEmptyStore(t *testing.T) {
	harness := startMemoryUI(t, nil, nil)
	requireHasText(t, harness.tester, "还没有记忆")
	harness.click(t, "全部清空")
	harness.click(t, "清空")
	requireToast(t, harness.notifier, "已清空")
	requireNoText(t, harness.tester, "清空失败")
	if got := len(harness.entries(t)); got != 0 {
		t.Fatalf("memories after clearing an empty store = %d, want 0", got)
	}
}

// faultMemoryStore is a contracts.MemoryStore with list/remove fault injection.
type faultMemoryStore struct {
	mu         sync.Mutex
	entries    []contracts.Memory
	listErr    error
	failRemove bool
}

func (s *faultMemoryStore) List(context.Context) ([]contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]contracts.Memory(nil), s.entries...), nil
}

func (s *faultMemoryStore) Add(_ context.Context, entry contracts.Memory) (contracts.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.ID = "fault-" + itoaSmall(len(s.entries))
	s.entries = append(s.entries, entry)
	return entry, nil
}

func (s *faultMemoryStore) Remove(_ context.Context, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failRemove {
		return false
	}
	for index, entry := range s.entries {
		if entry.ID == id {
			s.entries = append(s.entries[:index], s.entries[index+1:]...)
			return true
		}
	}
	return false
}

func (s *faultMemoryStore) Search(ctx context.Context, query string) ([]contracts.Memory, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	matches := make([]contracts.Memory, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Text), strings.ToLower(strings.TrimSpace(query))) {
			matches = append(matches, entry)
		}
	}
	return matches, nil
}

// TestMemoryManagementFailuresAreReadable covers store failures: list errors and
// failed removes surface as the tab's danger line instead of panicking.
func TestMemoryManagementFailuresAreReadable(t *testing.T) {
	// List failures: both the pane and the clear action report the read error.
	listFault := &faultMemoryStore{listErr: errors.New("memory read denied")}
	harness := startMemoryUI(t, nil, listFault)
	requireHasText(t, harness.tester, "读取记忆失败")
	harness.click(t, "整理记忆")
	harness.click(t, "整理")
	requireHasText(t, harness.tester, "读取记忆失败")
	harness.click(t, "全部清空")
	harness.click(t, "清空")
	requireHasText(t, harness.tester, "清空失败")

	// Remove failures: the notices report how many entries could not be removed.
	removeFault := &faultMemoryStore{entries: []contracts.Memory{
		memorySeed(0, "dup"), memorySeed(1, "dup"), memorySeed(2, "solo"),
	}, failRemove: true}
	harness = startMemoryUI(t, nil, removeFault)
	harness.click(t, "整理记忆")
	harness.click(t, "整理")
	requireHasText(t, harness.tester, "整理记忆失败：1")
	harness.click(t, "全部清空")
	harness.click(t, "清空")
	requireHasText(t, harness.tester, "清空失败：3")
	if got := len(removeFault.entries); got != 3 {
		t.Fatalf("fault store entries = %d, want 3 after failed removes", got)
	}
}
