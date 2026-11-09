package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// normalizeMemoryText is the duplicate key of the Memory tab's tidy action:
// trimmed, inner whitespace collapsed and case-insensitive.
func normalizeMemoryText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

// requestMemoryRefresh asks the next frame to reread the memory store after a
// mutation; the read model is otherwise cached in state and the frame builder
// never touches the store while nothing changed.
func requestMemoryRefresh(st *state) { st.memoryRefresh = true }

// buildMemoryTools is the memory management row: the entry count, the tidy
// action and the clear-all action. Both are destructive, so both confirm in a
// native AlertDialog first (see buildMemoryConfirmDialog) and report their
// outcome as a transient toast, never a resident badge.
func buildMemoryTools(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.MemoryStore, st *state, count int) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, st.tr(fmt.Sprintf("%d 条记忆", count), fmt.Sprintf("%d memories", count))),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
		if ui.Button(c, st.tr("整理记忆", "Tidy memories")).
			Tooltip(st.tr("删除重复的记忆，保留最早的一条", "Remove duplicate memories, keeping the earliest")).Clicked() {
			st.memoryConfirm = "tidy"
			runtime.Invalidate()
		}
		clear := ui.Button(c, st.tr("全部清空", "Clear all")).
			Tooltip(st.tr("删除全部记忆", "Delete every memory")).TextColor(t.DangerText)
		if clear.Clicked() {
			st.memoryClearOpen = true
			runtime.Invalidate()
		}
	})
	buildMemoryConfirmDialog(c, t, runtime, store, st)
	buildMemoryClearDialog(c, t, runtime, store, st)
}

// buildMemoryConfirmDialog is the native confirmation of the memory
// management's destructive actions: the duplicate tidy and one entry's delete.
func buildMemoryConfirmDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.MemoryStore, st *state) {
	if st.memoryConfirm == "" {
		return
	}
	open := true
	title, message, confirm := "", "", ""
	action := st.memoryConfirm
	switch {
	case action == "tidy":
		title = st.tr("整理记忆", "Tidy memories")
		message = st.tr("确认删除重复的记忆吗？每组重复只保留最早的一条。",
			"Remove duplicate memories? The earliest of each group is kept.")
		confirm = st.tr("整理", "Tidy")
	default:
		title = st.tr("删除记忆", "Delete memory")
		message = st.tr("确认删除这条记忆吗？此操作无法撤销。",
			"Delete this memory? This cannot be undone.")
		confirm = st.tr("删除", "Delete")
	}
	switch ui.AlertDialog(c, &open, title, message, st.tr("取消", "Cancel"), confirm) {
	case 1:
		if action == "tidy" {
			tidyMemories(store, st)
			runtime.Invalidate()
		} else {
			// Keep the full entry so the success toast's Undo can Add it back.
			var removed contracts.Memory
			for _, entry := range st.memories {
				if entry.ID == action {
					removed = entry
					break
				}
			}
			removeMemory(store, st, removed, action)
			runtime.Invalidate()
		}
	}
	if !open {
		st.memoryConfirm = ""
	}
}

// tidyMemories removes duplicate memories, keeping the earliest of each
// normalized text; the outcome is a transient toast, failures the tab's danger
// line.
func tidyMemories(store contracts.MemoryStore, st *state) {
	ctx := context.Background()
	memories, err := store.List(ctx)
	if err != nil {
		st.memoryError = st.tr("读取记忆失败：", "Failed to read memory: ") + err.Error()
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
	requestMemoryRefresh(st)
	switch {
	case failures > 0:
		st.memoryError = st.tr("整理记忆失败：", "Tidy failed: ") + fmt.Sprintf("%d", failures)
	case removed > 0:
		st.memoryError = ""
		st.toast(contracts.NotifySuccess, st.tr(fmt.Sprintf("已整理 %d 条", removed), fmt.Sprintf("Tidied %d memories", removed)))
	default:
		st.memoryError = ""
		st.toast(contracts.NotifyInfo, st.tr("没有可整理的重复记忆", "No duplicate memories to tidy"))
	}
}

// removeMemory deletes one memory entry, offers Undo (the host's ActionNotifier
// re-adds the saved entry) and reports the outcome.
func removeMemory(store contracts.MemoryStore, st *state, entry contracts.Memory, id string) {
	if id == "" {
		return
	}
	if !store.Remove(context.Background(), id) {
		st.memoryError = st.tr("删除记忆失败：", "Delete failed: ") + firstNonEmpty(id, st.tr("未知条目", "unknown entry"))
		requestMemoryRefresh(st)
		return
	}
	st.memoryError = ""
	requestMemoryRefresh(st)
	if strings.TrimSpace(entry.Text) == "" {
		st.toast(contracts.NotifySuccess, st.tr("已删除记忆", "Memory deleted"))
		return
	}
	st.notifyUndo(st.tr("已删除记忆", "Memory deleted"), st.tr("撤销", "Undo"), func() {
		if _, err := store.Add(context.Background(), entry); err != nil {
			return
		}
		requestMemoryRefresh(st)
		st.runtime.Invalidate()
	})
}

// clearMemories removes every memory; the outcome is a transient toast.
func clearMemories(store contracts.MemoryStore, st *state) {
	ctx := context.Background()
	memories, err := store.List(ctx)
	if err != nil {
		st.memoryError = st.tr("清空失败：", "Clear failed: ") + err.Error()
		return
	}
	failures := 0
	for _, memory := range memories {
		if !store.Remove(ctx, memory.ID) {
			failures++
		}
	}
	requestMemoryRefresh(st)
	if failures > 0 {
		st.memoryError = st.tr("清空失败：", "Clear failed: ") + fmt.Sprintf("%d", failures)
		return
	}
	st.memoryError = ""
	removed := append([]contracts.Memory(nil), memories...)
	if len(removed) == 0 {
		st.toast(contracts.NotifySuccess, st.tr("已清空", "Cleared"))
		return
	}
	st.notifyUndo(
		st.tr(fmt.Sprintf("已清空 %d 条记忆", len(removed)), fmt.Sprintf("Cleared %d memories", len(removed))),
		st.tr("撤销", "Undo"), func() {
			for _, entry := range removed {
				_, _ = store.Add(context.Background(), entry)
			}
			requestMemoryRefresh(st)
			st.runtime.Invalidate()
		})
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

// buildMemoryEditDialog is the native editor for one memory entry: it updates
// the entry through contracts.MemoryUpdater (the same text, the same scope) and
// refreshes the cached read model on success. Backends without the update half
// simply never show the edit action.
func buildMemoryEditDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.MemoryStore, st *state) {
	if !st.memoryEditOpen {
		return
	}
	updater, ok := store.(contracts.MemoryUpdater)
	if !ok {
		st.memoryEditOpen = false
		st.memoryEditErr = ""
		return
	}
	save := func() {
		text := strings.TrimSpace(st.memoryEditText)
		if text == "" || st.memoryEditID == "" {
			return
		}
		if _, err := updater.Update(context.Background(), st.memoryEditID, text, ""); err != nil {
			st.memoryEditErr = st.tr("更新记忆失败：", "Update memory failed: ") + err.Error()
			runtime.Invalidate()
			return
		}
		st.memoryEditOpen = false
		st.memoryEditID = ""
		st.memoryEditText = ""
		st.memoryEditErr = ""
		st.toast(contracts.NotifySuccess, st.tr("已更新记忆", "Memory updated"))
		requestMemoryRefresh(st)
		runtime.Invalidate()
	}
	ui.Modal(c, &st.memoryEditOpen, func() {
		dialogEnter(c, save)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("编辑记忆", "Edit memory")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"修改这条记忆的内容；作用域和创建时间保持不变。",
				"Change this memory's text; its scope and creation time are kept.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextInput(c, &st.memoryEditText).Label(st.tr("记忆内容", "Memory text")).FillWidth().
				OnTextKey(enterTo(save))
			if st.memoryEditErr != "" {
				buildDialogError(c, t, st.memoryEditErr)
			}
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					st.memoryEditOpen = false
					st.memoryEditID = ""
					st.memoryEditText = ""
					st.memoryEditErr = ""
					runtime.Invalidate()
				}},
				{Label: st.tr("保存", "Save"), Primary: true,
					Disabled: strings.TrimSpace(st.memoryEditText) == "",
					OnClick:  save},
			})
		})
	})
}

// memoryRow is one memory entry as a divider row (not a card): the body text,
// the scope/created metadata and a row-end delete icon that confirms first.
func memoryRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.MemoryStore, st *state, memory contracts.Memory) {
	th := t.Theme()
	ui.Column(c.Key("session.memory." + memory.ID)).FillWidth().Gap(design.Space1).
		PaddingY(design.Space2).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Text(c, memory.Text), t.Font(design.TypeBody)).
				TextColor(t.LabelPrimary).Grow(1)
			if _, editable := store.(contracts.MemoryUpdater); editable {
				edit := ui.Button(c, "").Label(st.tr("编辑记忆", "Edit memory")).
					Tooltip(st.tr("编辑这条记忆", "Edit this memory")).
					Size(th.Space(6), th.Space(6)).PaddingX(0).Radius(design.RadiusSM)
				edit.Children(func() {
					ui.Icon(c, icons.Pencil()).Size(design.Space3, design.Space3).
						TextColor(t.LabelTertiary)
				})
				if edit.Clicked() {
					st.memoryEditOpen = true
					st.memoryEditID = memory.ID
					st.memoryEditText = memory.Text
					st.memoryEditErr = ""
					runtime.Invalidate()
				}
			}
			remove := ui.Button(c, "").Label(st.tr("删除记忆", "Delete memory")).
				Tooltip(st.tr("删除这条记忆", "Delete this memory")).
				Size(th.Space(6), th.Space(6)).PaddingX(0).Radius(design.RadiusSM)
			remove.Children(func() {
				ui.Icon(c, icons.Trash()).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			})
			if remove.Clicked() {
				st.memoryConfirm = memory.ID
				runtime.Invalidate()
			}
		})
		meta := formatTimestamp(st.locale, memory.Created)
		if scope := memoryScopeLabel(st, memory.Scope); scope != "" {
			meta = scope + " · " + meta
		}
		design.Apply(ui.Text(c, meta), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
	})
	ui.Divider(c)
}
