package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// normalizeMemoryText is the duplicate key of the Memory tab's tidy action:
// trimmed, inner whitespace collapsed and case-insensitive.
func normalizeMemoryText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

// buildMemoryTools is the memory management card: the entry count, the native
// tidy action and the clear-all action with its confirmation dialog.
func buildMemoryTools(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.MemoryStore, st *state, count int) {
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.tr(fmt.Sprintf("%d 条记忆", count), fmt.Sprintf("%d memories", count))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
			if ui.Button(c, st.tr("整理记忆", "Tidy")).
				Tooltip(st.tr("删除重复的记忆，保留最早的一条", "Remove duplicate memories, keeping the earliest")).Clicked() {
				tidyMemories(store, st)
				runtime.Invalidate()
			}
			clear := ui.Button(c, st.tr("全部清空", "Clear all")).
				Tooltip(st.tr("删除全部记忆", "Delete every memory")).TextColor(t.DangerText)
			if clear.Clicked() {
				st.memoryClearOpen = true
				runtime.Invalidate()
			}
		})
		if st.memoryNotice != "" {
			design.Apply(ui.Badge(c, st.memoryNotice), t.Badge()).TextColor(t.Success)
		}
	})
	buildMemoryClearDialog(c, t, runtime, store, st)
}

// tidyMemories removes duplicate memories, keeping the earliest of each
// normalized text; failures land in the tab's danger line.
func tidyMemories(store contracts.MemoryStore, st *state) {
	ctx := context.Background()
	memories, err := store.List(ctx)
	if err != nil {
		st.memoryError = st.tr("读取记忆失败：", "Failed to read memory: ") + err.Error()
		st.memoryNotice = ""
		return
	}
	sort.SliceStable(memories, func(i, j int) bool {
		if memories[i].Created.Equal(memories[j].Created) {
			return false
		}
		return memories[i].Created.Before(memories[j].Created)
	})
	seen := make(map[string]bool, len(memories))
	removed, failures := 0, 0
	for _, memory := range memories {
		key := normalizeMemoryText(memory.Text)
		if seen[key] {
			if store.Remove(ctx, memory.ID) {
				removed++
			} else {
				failures++
			}
			continue
		}
		seen[key] = true
	}
	switch {
	case failures > 0:
		st.memoryNotice = ""
		st.memoryError = st.tr("整理记忆失败：", "Tidy failed: ") + fmt.Sprintf("%d", failures)
	case removed > 0:
		st.memoryError = ""
		st.memoryNotice = st.tr(fmt.Sprintf("已整理 %d 条", removed), fmt.Sprintf("Tidied %d memories", removed))
	default:
		st.memoryError = ""
		st.memoryNotice = st.tr("没有可整理的重复记忆", "No duplicate memories to tidy")
	}
}

// clearMemories removes every memory; failures land in the tab's danger line.
func clearMemories(store contracts.MemoryStore, st *state) {
	ctx := context.Background()
	memories, err := store.List(ctx)
	if err != nil {
		st.memoryNotice = ""
		st.memoryError = st.tr("清空失败：", "Clear failed: ") + err.Error()
		return
	}
	failures := 0
	for _, memory := range memories {
		if !store.Remove(ctx, memory.ID) {
			failures++
		}
	}
	if failures > 0 {
		st.memoryNotice = ""
		st.memoryError = st.tr("清空失败：", "Clear failed: ") + fmt.Sprintf("%d", failures)
		return
	}
	st.memoryError = ""
	st.memoryNotice = st.tr("已清空", "Cleared")
}

// buildMemoryClearDialog is the native clear-all confirmation.
func buildMemoryClearDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.MemoryStore, st *state) {
	if !st.memoryClearOpen {
		return
	}
	message := st.tr("确认删除全部记忆吗？此操作无法撤销。",
		"Delete all memories? This cannot be undone.")
	switch ui.AlertDialog(c, &st.memoryClearOpen,
		st.tr("清空记忆", "Clear memories"), message,
		st.tr("取消", "Cancel"), st.tr("清空", "Clear")) {
	case 1:
		clearMemories(store, st)
		runtime.Invalidate()
	default:
		runtime.Invalidate()
	}
}
