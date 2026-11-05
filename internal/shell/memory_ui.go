package shell

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/store"
)

// settingsTabMemory is the Settings > Memory page. models_ui.go owns the
// settingsTab enum and is outside this change's ownership, so the value simply
// follows the About page.
const settingsTabMemory settingsTab = settingsTabAbout + 1

// memoryIcon is the nav cell's note glyph, drawn to the same 16x16 line-icon
// geometry as the other settings icons.
var memoryIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round"><path d="M3.2 2.4h9.6v11.2H3.2z"/><path d="M5.4 5.4h5.2M5.4 8h5.2M5.4 10.6h3.2"/></svg>`))

// memorySettingsPage is Settings > Memory: the current workspace's persistent
// notes, with delete, clear-all and refresh. The store is the only source of
// truth, so the list is read on every frame instead of being cached in a
// schema-less setting.
func (a *App) memorySettingsPage(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Gap(design.Space3).Children(func() {
		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Start).Gap(design.Space3).Children(func() {
			ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
				settingsText(c, a.memoryText("Workspace memory", "工作区记忆"),
					design.TypeM, t.LabelPrimary).FontWeight(600)
				settingsText(c, a.memoryText(
					"Persistent notes the agent adds with the memory tool. They are injected into future turns for this workspace only.",
					"agent 通过 memory 工具添加的持久笔记。它们只会注入到当前工作区未来的回合中。"),
					design.TypeXXS, t.LabelTertiary)
			})
			ui.Row(c).Gap(design.Space2).Children(func() {
				a.memoryButton(c, t, a.memoryText("Refresh", "刷新"),
					a.memoryText("Refresh memories", "刷新记忆"), false, func() {
						c.Invalidate()
						c.Toast(a.memoryText("Memories refreshed", "记忆已刷新"))
					})
				a.memoryButton(c, t, a.memoryText("Clear all", "全部清除"),
					a.memoryText("Clear all memories", "清除全部记忆"), true, func() {
						a.clearWorkspaceMemories(c)
					})
			})
		})

		st := a.memoryStore()
		if st == nil {
			settingsText(c, a.memoryText(
				"Memory requires the live session service.",
				"记忆功能需要运行中的会话服务。"), design.TypeXXS, t.LabelTertiary)
			return
		}
		workspaceID := ""
		if a.Ctrl != nil {
			workspaceID = strings.TrimSpace(a.Ctrl.CurrentWorkspaceID())
		}
		if workspaceID == "" {
			settingsText(c, a.memoryText(
				"Select a workspace to view its memories.",
				"请选择工作区以查看其记忆。"), design.TypeXXS, t.LabelTertiary)
			return
		}

		memories, err := st.ListMemories(workspaceID, store.MaxMemoryLimit)
		if err != nil {
			settingsText(c, a.memoryText(
				"Could not load workspace memories.",
				"无法加载工作区记忆。"), design.TypeXXS, t.Danger)
			return
		}
		if len(memories) == 0 {
			a.memoryEmpty(c, t)
			return
		}
		ui.Column(c).Gap(design.Space1).Children(func() {
			for _, memory := range memories {
				a.memoryRow(c, t, memory)
			}
		})
	})
}

// memoryEmpty is the empty state for a workspace without notes.
func (a *App) memoryEmpty(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Padding(design.Space6, 0, design.Space6, 0).AlignItems(ui.Center).
		Gap(design.Space2).Children(func() {
		settingsText(c, a.memoryText("No memories in this workspace yet.", "此工作区还没有记忆。"),
			design.TypeBody, t.LabelPrimary)
		settingsText(c, a.memoryText(
			"Ask the agent to remember durable facts, conventions, or decisions with the memory tool.",
			"让 agent 使用 memory 工具记住持久的事实、约定或决策。"),
			design.TypeXXS, t.LabelTertiary)
	})
}

// memoryRow is one note: its text and update time on the left, Delete on the
// right. The delete button carries the memory id in its accessible name so a
// list with several notes stays unambiguous to accessibility and tests.
func (a *App) memoryRow(c *ui.Context, t *design.Tokens, memory store.Memory) {
	id := memory.ID
	ui.Row(c).Padding(design.Space3, 0, design.Space3, 0).Gap(design.Space3).
		AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Column(c).Grow(1).Padding(0, settingsTextEnd, 0, 0).
			Gap(design.Space1).Children(func() {
			settingsText(c, memoryDisplayText(memory.Content), design.TypeBody, t.LabelPrimary).MaxLines(2)
			settingsText(c, a.memoryText("Updated ", "更新于 ")+
				memory.UpdatedAt.Local().Format("2006-01-02 15:04"),
				design.TypeXXS, t.LabelTertiary)
		})
		a.memoryButton(c, t, a.memoryText("Delete", "删除"),
			a.memoryText("Delete memory ", "删除记忆 ")+id, true, func() {
				a.deleteWorkspaceMemory(c, id)
			})
	})
}

// memoryButton is a small bordered action used by the Memory page.
func (a *App) memoryButton(c *ui.Context, t *design.Tokens, label, accessible string, danger bool, fn func()) {
	color := t.LabelPrimary
	border := t.BorderL3
	if danger {
		color = t.Danger
		border = t.Danger
	}
	el := ui.Row(c).Height(28).PaddingX(10).Radius(design.RadiusSM).
		Border(1, border).AlignItems(ui.Center).
		Label(accessible).Tooltip(label).Role(ui.RoleButton).Focusable()
	if el.Hovered() || el.Focused() {
		el = el.Background(t.BgHover)
	}
	el.Children(func() {
		settingsText(c, label, design.TypeXXS, color).NoWrap()
	})
	el.OnClick(fn)
}

// memoryStore is the live store, or nil when the shell is static.
func (a *App) memoryStore() *store.Store {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.Store()
}

// deleteWorkspaceMemory removes one note. The id came from the workspace-scoped
// list above, so no further scope check is needed in the UI.
func (a *App) deleteWorkspaceMemory(c *ui.Context, id string) {
	st := a.memoryStore()
	if st == nil {
		return
	}
	if err := st.DeleteMemory(id); err != nil {
		c.Toast(a.memoryText("Could not delete the memory.", "无法删除该记忆。"))
		return
	}
	c.Toast(a.memoryText("Memory deleted", "记忆已删除"))
}

// clearWorkspaceMemories empties the current workspace's memory list. Other
// workspaces are untouched by the store call.
func (a *App) clearWorkspaceMemories(c *ui.Context) {
	st := a.memoryStore()
	if st == nil || a.Ctrl == nil {
		return
	}
	workspaceID := strings.TrimSpace(a.Ctrl.CurrentWorkspaceID())
	if workspaceID == "" {
		c.Toast(a.memoryText("Select a workspace first.", "请先选择工作区。"))
		return
	}
	cleared, err := st.ClearMemories(workspaceID)
	if err != nil {
		c.Toast(a.memoryText("Could not clear workspace memories.", "无法清除工作区记忆。"))
		return
	}
	if cleared == 0 {
		c.Toast(a.memoryText("No memories to clear", "没有可清除的记忆"))
		return
	}
	c.Toast(a.memoryText(
		fmt.Sprintf("Cleared %d memories", cleared),
		fmt.Sprintf("已清除 %d 条记忆", cleared)))
}

// memoryText selects the UI language for strings added by this page without
// extending the shared i18n catalog.
func (a *App) memoryText(en, zh string) string {
	if a.lang() == LangZH {
		return zh
	}
	return en
}

// memoryDisplayText collapses a stored note to one readable line for the row.
func memoryDisplayText(content string) string {
	return strings.Join(strings.Fields(content), " ")
}
