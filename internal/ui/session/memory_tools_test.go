package session_test

// TDD tests (RED first) for the Memory tab's management actions: tidy
// duplicates (keep the earliest) and clear-all with a native confirmation.

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "整理记忆")
	h.assertTexts(tester, "已整理 2 条")
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
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)
	h.click(tester, "整理记忆")
	h.assertTexts(tester, "没有可整理的重复记忆")
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
	h2.assertTexts(tester2, "整理记忆失败")
}

// --- clear all --------------------------------------------------------------

func TestMemoryClearAllConfirmed(t *testing.T) {
	store := newFakeMemoryStore(
		memory("mem-1", "Alpha"),
		memory("mem-2", "Beta"),
		memory("mem-3", "Gamma"),
	)
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	openMemoryTab(t, h, tester)

	h.click(tester, "全部清空")
	h.assertTexts(tester, "清空记忆", "取消", "清空")
	h.click(tester, "清空")
	h.assertTexts(tester, "已清空")
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
	h.assertTexts(tester, "清空失败")
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
	h := newHarnessWithMemory(t, tidyChat(), store)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "Memory")

	h.assertTexts(tester, "2 memories", "Tidy", "Clear all")
	h.click(tester, "Tidy")
	h.assertTexts(tester, "No duplicate memories to tidy")
}
