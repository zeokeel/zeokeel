package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// M4 memory layout (docs/design/auto-memory.md §6, revised by the user):
//
//   - Settings › Memory is the Global memory page: machine-wide preferences
//     shared by every workspace. It has no workspace tabs and never reads a
//     workspace's rows.
//   - The session header's Memory tab (always present beside Chat; Trajectory
//     sits between them while Show coding view is on) manages the current
//     workspace's memories for that chat.
//
// Both surfaces render the same manager — mode, counts, pending inbox, active
// list, Add/Edit modals, scoped Clear all and markdown export — through one set
// of scope-parameterized components, so the two hosts cannot drift apart.

const settingsTabMemory settingsTab = settingsTabAbout + 1

// memoryIcon is the nav cell's note glyph, drawn to the same 16x16 line-icon
// geometry as the other settings icons.
var memoryIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round"><path d="M3.2 2.4h9.6v11.2H3.2z"/><path d="M5.4 5.4h5.2M5.4 8h5.2M5.4 10.6h3.2"/></svg>`))

// memoryModeValues is the ordered Off/Suggest/Auto mode catalog. The values are
// the persisted setting values (session.MemoryMode*).
var memoryModeValues = []string{
	session.MemoryModeOff,
	session.MemoryModeSuggest,
	session.MemoryModeAuto,
}

// memorySurface names one host's scope: the Settings global page (WorkspaceID
// empty) or a session's workspace Memory tab.
type memorySurface struct {
	Scope       string
	WorkspaceID string
}

// memoryPanelState is the modal interaction state of one surface: the Add and
// Edit dialogs, and the Clear all confirmation's target scope.
type memoryPanelState struct {
	addOpen          bool
	addDraft         string
	editOpen         bool
	editID           string
	editDraft        string
	clearOpen        bool
	clearScope       string
	clearWorkspaceID string
}

// memoryPanels is the per-App modal state of both surfaces. It is kept in a
// package map keyed by App (like the shell's other view state) so a screen
// switch keeps each surface's open dialog.
type memoryPanels struct {
	global    memoryPanelState
	workspace memoryPanelState
}

var memoryPanelStates sync.Map // map[*App]*memoryPanels

// memoryPanelsFor returns the modal state of a, making it on first use.
func (a *App) memoryPanelsFor() *memoryPanels {
	if raw, ok := memoryPanelStates.Load(a); ok {
		return raw.(*memoryPanels)
	}
	panels := &memoryPanels{}
	actual, _ := memoryPanelStates.LoadOrStore(a, panels)
	return actual.(*memoryPanels)
}

// openMemorySettings opens Settings on the Memory page (the global surface).
func (a *App) openMemorySettings() {
	a.shellUI().settingsTab = settingsTabMemory
	a.openSettings()
}

// openMemoryFromCommand implements /memory: inside a session it selects the
// session's Memory tab (workspace scope); on Home or without a session it opens
// the global Settings › Memory page.
func (a *App) openMemoryFromCommand() {
	if a.Ctrl != nil && a.Screen == Session && strings.TrimSpace(a.Ctrl.CurrentSessionID()) != "" {
		a.uiState().liveTabCount = len(a.liveTabsLayout())
		if position := a.liveTabPosition(liveMemory); position >= 0 {
			a.uiState().tab = position
		}
		return
	}
	a.openMemorySettings()
}

// memorySettingsPage is Settings › Memory: the global memory page.
func (a *App) memorySettingsPage(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	surface := memorySurface{Scope: store.MemoryScopeGlobal}
	panel := &a.memoryPanelsFor().global
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		ui.Column(c).Gap(th.Space(1)).Children(func() {
			settingsText(c, a.memoryText("Global memory", "全局记忆"),
				design.TypeL, t.LabelPrimary).FontWeight(600)
			settingsText(c, a.memoryText(
				"Machine-wide notes the agent injects into every session. Review suggestions, add, edit or delete them here.",
				"agent 会注入到所有会话的机器级笔记。在这里审核建议、添加、编辑或删除。"),
				design.TypeXS, t.LabelTertiary).MaxLines(3)
		})
		a.memoryManager(c, t, surface, panel)
	})
	a.memoryAddDialog(c, t, surface, panel)
	a.memoryEditDialog(c, t, panel)
	a.memoryClearDialog(c, t, surface, panel)
}

// sessionMemoryPane is the session's Memory tab: the current workspace's
// memory manager, in the same body frame as Chat and Trajectory.
func (a *App) sessionMemoryPane(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	panel := &a.memoryPanelsFor().workspace
	workspace, hasWorkspace := a.memoryWorkspace()
	surface := memorySurface{Scope: store.MemoryScopeWorkspace, WorkspaceID: workspace.ID}
	ui.Column(c).Grow(1).AlignItems(ui.Center).Padding(th.Space(4)).Children(func() {
		ui.Column(c).Width(a.trajectoryColumnWidth(c, t)).Grow(1).Children(func() {
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(th.Space(4)).Padding(0, 0, th.Space(4), 0).Children(func() {
					ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Start).
						Gap(th.Space(3)).Children(func() {
						ui.Column(c).Grow(1).Gap(th.Space(1)).Children(func() {
							settingsText(c, a.memoryText("Workspace memory", "工作区记忆"),
								design.TypeM, t.LabelPrimary).FontWeight(600)
							settingsText(c, a.memoryText(
								"Notes the agent injects into this workspace's turns only.",
								"agent 仅注入到当前工作区回合的笔记。"),
								design.TypeXS, t.LabelTertiary).MaxLines(2)
						})
						a.memoryButton(c, t, a.memoryText("Global memory…", "全局记忆…"),
							a.memoryText("Open global memory settings", "打开全局记忆设置"),
							false, func() { a.openMemorySettings() })
					})
					if !hasWorkspace {
						settingsText(c, a.memoryText(
							"Select a workspace to view its workspace memory.",
							"请选择工作区以查看其工作区记忆。"), design.TypeXS, t.LabelTertiary)
						return
					}
					a.memoryManager(c, t, surface, panel)
				})
			})
		})
	})
	a.memoryAddDialog(c, t, surface, panel)
	a.memoryEditDialog(c, t, panel)
	a.memoryClearDialog(c, t, surface, panel)
}

// memoryManager is the shared control surface: mode, counts/actions, the
// pending inbox and the active list, for the surface's scope.
func (a *App) memoryManager(c *ui.Context, t *design.Tokens, surface memorySurface, panel *memoryPanelState) {
	a.memoryModeRow(c, t, surface)
	a.memorySummaryRow(c, t, surface, panel)
	a.memoryPendingSection(c, t, surface)
	a.memoryActiveSection(c, t, surface, panel)
}

// memoryModeRow is the surface's Off/Suggest/Auto control and its explanation.
// The segmented-looking radio group writes that scope's own Settings field, so
// changing one scope never changes the other.
func (a *App) memoryModeRow(c *ui.Context, t *design.Tokens, surface memorySurface) {
	th := t.Theme()
	mode := a.memoryMode(surface)
	ui.Column(c).Gap(th.Space(2)).Children(func() {
		ui.Row(c).Gap(th.Space(3)).AlignItems(ui.Center).Children(func() {
			settingsText(c, a.memoryText("Memory mode", "记忆模式"),
				design.TypeS, t.LabelPrimary)
			for _, value := range memoryModeValues {
				value := value
				selected := mode == value
				label := a.memoryModeLabel(value)
				button := ui.ButtonBase(c).Height(th.Space(8)).PaddingX(th.Space(3)).
					Radius(design.RadiusSM).AlignItems(ui.Center).
					Role(ui.RoleRadio).Checked(selected).Focusable().
					Label(label).Tooltip(label)
				switch {
				case selected:
					button = button.Background(t.BgStrong).Border(1, t.BorderL3)
				case button.Hovered():
					button = button.Background(t.BgHover)
				}
				button.Children(func() {
					color, weight := t.LabelSecondary, 400
					if selected {
						color, weight = t.LabelPrimary, 600
					}
					nativeText(c, design.TypeXS, color, label).FontWeight(weight).NoWrap()
				})
				button.OnClick(func() { a.setMemoryMode(c, surface, value) })
			}
		})
		settingsText(c, a.memoryModeDescription(mode), design.TypeXS, t.LabelTertiary)
	})
}

// memoryMode resolves the persisted mode of one surface, with the design's
// defaults when Settings has not stored it (workspace auto, global suggest).
func (a *App) memoryMode(surface memorySurface) string {
	cfg := a.currentSettings()
	mode := cfg.MemoryWorkspaceMode
	if surface.Scope == store.MemoryScopeGlobal {
		mode = cfg.MemoryGlobalMode
	}
	switch mode {
	case session.MemoryModeOff, session.MemoryModeSuggest, session.MemoryModeAuto:
		return mode
	}
	if surface.Scope == store.MemoryScopeGlobal {
		return session.MemoryModeSuggest
	}
	return session.MemoryModeAuto
}

// setMemoryMode persists one surface's mode through the Settings store.
func (a *App) setMemoryMode(c *ui.Context, surface memorySurface, mode string) {
	if a.memoryMode(surface) == mode {
		return
	}
	a.updateSettings(c, func(cfg *session.Settings) {
		if surface.Scope == store.MemoryScopeGlobal {
			cfg.MemoryGlobalMode = mode
		} else {
			cfg.MemoryWorkspaceMode = mode
		}
	})
}

// memoryModeLabel is the visible label of one mode value.
func (a *App) memoryModeLabel(mode string) string {
	switch mode {
	case session.MemoryModeOff:
		return a.memoryText("Off", "关闭")
	case session.MemoryModeSuggest:
		return a.memoryText("Suggest", "建议")
	default:
		return a.memoryText("Auto", "自动")
	}
}

// memoryModeDescription explains what the selected mode does.
func (a *App) memoryModeDescription(mode string) string {
	switch mode {
	case session.MemoryModeOff:
		return a.memoryText("The agent will not save memories.", "agent 不会保存记忆。")
	case session.MemoryModeSuggest:
		return a.memoryText("The agent suggests memories for you to review.",
			"agent 会提出记忆建议，由你审核。")
	default:
		return a.memoryText("The agent saves useful memories automatically.",
			"agent 会自动保存有用的记忆。")
	}
}

// memorySummaryRow is the counts line plus the Add memory, Export and Clear all
// actions of the surface.
func (a *App) memorySummaryRow(c *ui.Context, t *design.Tokens, surface memorySurface, panel *memoryPanelState) {
	th := t.Theme()
	active, pending := 0, 0
	if db := a.memoryStore(); db != nil {
		var err error
		active, pending, err = db.MemoryCounts(surface.Scope, surface.WorkspaceID)
		if err != nil {
			settingsText(c, a.memoryText("Could not load memories.", "无法加载记忆。"),
				design.TypeXS, t.Danger)
			return
		}
	}
	ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).
		Gap(th.Space(3)).Children(func() {
		settingsText(c, fmt.Sprintf(
			a.memoryText("%d active · %d pending", "%d 条生效 · %d 条待审核"), active, pending),
			design.TypeS, t.LabelSecondary).FontWeight(600)
		ui.Row(c).Gap(th.Space(2)).Children(func() {
			a.memoryButton(c, t, a.memoryText("Add memory", "添加记忆"),
				a.memoryText("Add memory", "添加记忆"), false, func() {
					a.beginMemoryAdd(panel)
				})
			a.memoryButton(c, t, a.memoryText("Export markdown", "导出 Markdown"),
				a.memoryText("Export markdown", "导出 Markdown"), false, func() {
					a.exportMemories(c, surface)
				})
			a.memoryButton(c, t, a.memoryText("Clear all", "全部清除"),
				a.memoryText("Clear all memories", "清除全部记忆"), true, func() {
					a.beginMemoryClear(panel, surface)
				})
		})
	})
}

// memoryPendingSection is the pending inbox: suggestions the suggest mode left
// for review. Each row offers Accept (promote to active) and Discard (delete);
// an empty inbox draws nothing.
func (a *App) memoryPendingSection(c *ui.Context, t *design.Tokens, surface memorySurface) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	pending, err := db.ListMemoriesScoped(surface.Scope, surface.WorkspaceID, store.MemoryStatusPending, store.MaxMemoryLimit)
	if err != nil || len(pending) == 0 {
		return
	}
	th := t.Theme()
	ui.Column(c).Gap(th.Space(2)).Children(func() {
		settingsText(c, a.memoryText("Suggestions to review", "待审核建议"),
			design.TypeM, t.LabelPrimary).FontWeight(600)
		card := ui.Column(c).Background(t.BgStrong).Border(1, t.BorderL2).
			Radius(th.Radius)
		card.Children(func() {
			for i, memory := range pending {
				a.memoryPendingRow(c, t, memory, i == len(pending)-1)
			}
		})
	})
}

// memoryPendingRow is one pending suggestion: content and kind/source tags on
// the left, Accept and Discard on the right. The action labels carry the
// memory id so a list with several suggestions stays unambiguous to assistive
// technology and tests.
func (a *App) memoryPendingRow(c *ui.Context, t *design.Tokens, memory store.Memory, last bool) {
	th := t.Theme()
	id := memory.ID
	row := ui.Row(c).Padding(th.Space(4), 0).Gap(th.Space(3)).
		MinHeight(th.Space(16)).AlignItems(ui.Center)
	if !last {
		row = row.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).Gap(th.Space(2)).Children(func() {
			settingsText(c, memoryDisplayText(memory.Content), design.TypeBody, t.LabelPrimary).
				MaxLines(2)
			ui.Row(c).Gap(th.Space(1.5)).AlignItems(ui.Center).Children(func() {
				a.memoryTag(c, t, a.memoryKindLabel(memory.Kind))
				a.memoryTag(c, t, a.memorySourceLabel(memory.Source))
			})
		})
		ui.Row(c).Gap(th.Space(2)).Children(func() {
			a.memoryButton(c, t, a.memoryText("Accept", "采纳"),
				a.memoryText("Accept memory ", "采纳记忆 ")+id, false, func() {
					a.acceptMemory(c, id)
				})
			a.memoryButton(c, t, a.memoryText("Discard", "丢弃"),
				a.memoryText("Discard memory ", "丢弃记忆 ")+id, true, func() {
					a.discardMemory(c, id)
				})
		})
	})
}

// memoryActiveSection is the saved-memory list of the surface, or the empty
// state.
func (a *App) memoryActiveSection(c *ui.Context, t *design.Tokens, surface memorySurface, panel *memoryPanelState) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	active, err := db.ListMemoriesScoped(surface.Scope, surface.WorkspaceID, store.MemoryStatusActive, store.MaxMemoryLimit)
	if err != nil {
		settingsText(c, a.memoryText("Could not load memories.", "无法加载记忆。"),
			design.TypeXS, t.Danger)
		return
	}
	th := t.Theme()
	ui.Column(c).Gap(th.Space(2)).Children(func() {
		settingsText(c, a.memoryText("Saved memories", "已保存记忆"),
			design.TypeM, t.LabelPrimary).FontWeight(600)
		if len(active) == 0 {
			a.memoryEmpty(c, t)
			return
		}
		card := ui.Column(c).Background(t.BgStrong).Border(1, t.BorderL2).
			Radius(th.Radius)
		card.Children(func() {
			for i, memory := range active {
				a.memoryActiveRow(c, t, memory, i == len(active)-1, panel)
			}
		})
	})
}

// memoryActiveRow is one saved note: content and metadata on the left, Edit and
// Delete on the right.
func (a *App) memoryActiveRow(c *ui.Context, t *design.Tokens, memory store.Memory, last bool, panel *memoryPanelState) {
	th := t.Theme()
	id := memory.ID
	row := ui.Row(c).Padding(th.Space(4), 0).Gap(th.Space(3)).
		MinHeight(th.Space(14)).AlignItems(ui.Center)
	if !last {
		row = row.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).Padding(0, th.Space(4), 0, 0).
			Gap(th.Space(1)).Children(func() {
			settingsText(c, memoryDisplayText(memory.Content), design.TypeBody, t.LabelPrimary).
				MaxLines(2)
			ui.Row(c).Gap(th.Space(1.5)).AlignItems(ui.Center).Children(func() {
				a.memoryTag(c, t, a.memoryKindLabel(memory.Kind))
				a.memoryTag(c, t, a.memorySourceLabel(memory.Source))
				settingsText(c, a.memoryText("Updated ", "更新于 ")+
					memory.UpdatedAt.Local().Format("2006-01-02 15:04"),
					design.TypeXS, t.LabelTertiary)
			})
		})
		ui.Row(c).Gap(th.Space(2)).Children(func() {
			a.memoryButton(c, t, a.memoryText("Edit", "编辑"),
				a.memoryText("Edit memory ", "编辑记忆 ")+id, false, func() {
					a.beginMemoryEdit(panel, memory)
				})
			a.memoryButton(c, t, a.memoryText("Delete", "删除"),
				a.memoryText("Delete memory ", "删除记忆 ")+id, true, func() {
					a.deleteMemory(c, id)
				})
		})
	})
}

// beginMemoryAdd opens the Add memory dialog with an empty draft.
func (a *App) beginMemoryAdd(panel *memoryPanelState) {
	panel.addDraft = ""
	panel.addOpen = true
}

// memoryAddDialog is the Add memory form: a multi-line content editor on a
// native modal, with Cancel and Save.
func (a *App) memoryAddDialog(c *ui.Context, t *design.Tokens, surface memorySurface, panel *memoryPanelState) {
	th := t.Theme()
	ui.Modal(c, &panel.addOpen, func() {
		ui.Column(c).Width(th.Space(130)).Gap(th.Space(2)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary,
				a.memoryText("Add memory", "添加记忆")).FontWeight(600)
			nativeText(c, design.TypeXXS, t.LabelTertiary,
				a.memoryText("Memory content", "记忆内容"))
			ui.TextArea(c, &panel.addDraft).
				Label(a.memoryText("Memory content", "记忆内容")).
				Placeholder(a.memoryText("What should the agent remember?", "希望 agent 记住什么？")).
				Height(th.Space(23)).AutoFocus()
			ui.Row(c).Justify(ui.End).Gap(th.Space(2)).
				Padding(th.Space(2), 0, 0, 0).Children(func() {
				if ui.Button(c, a.memoryText("Cancel", "取消")).Clicked() {
					panel.addOpen = false
				}
				if ui.PrimaryButton(c, a.memoryText("Save", "保存")).Clicked() {
					a.confirmMemoryAdd(c, surface, panel)
				}
			})
		})
	})
}

// confirmMemoryAdd validates and stores a new manual memory in the surface's
// scope (kind project, source manual, status active).
func (a *App) confirmMemoryAdd(c *ui.Context, surface memorySurface, panel *memoryPanelState) {
	content, ok := a.validateMemoryContent(c, panel.addDraft)
	if !ok {
		return
	}
	db := a.memoryStore()
	if db == nil {
		panel.addOpen = false
		return
	}
	if _, err := db.CreateMemoryScoped(store.Memory{
		Scope:       surface.Scope,
		WorkspaceID: surface.WorkspaceID,
		Kind:        store.MemoryKindProject,
		Source:      store.MemorySourceManual,
		Status:      store.MemoryStatusActive,
		Content:     content,
	}); err != nil {
		c.Toast(a.memoryText("Could not save the memory.", "无法保存该记忆。"))
		return
	}
	panel.addOpen = false
	panel.addDraft = ""
	c.Toast(a.memoryText("Memory added", "记忆已添加"))
}

// beginMemoryEdit opens the Edit memory dialog prefilled with the row's text.
func (a *App) beginMemoryEdit(panel *memoryPanelState, memory store.Memory) {
	panel.editID = memory.ID
	panel.editDraft = memory.Content
	panel.editOpen = true
}

// memoryEditDialog is the Edit memory form: a prefilled multi-line editor.
func (a *App) memoryEditDialog(c *ui.Context, t *design.Tokens, panel *memoryPanelState) {
	th := t.Theme()
	ui.Modal(c, &panel.editOpen, func() {
		ui.Column(c).Width(th.Space(130)).Gap(th.Space(2)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary,
				a.memoryText("Edit memory", "编辑记忆")).FontWeight(600)
			nativeText(c, design.TypeXXS, t.LabelTertiary,
				a.memoryText("Memory content", "记忆内容"))
			ui.TextArea(c, &panel.editDraft).
				Label(a.memoryText("Memory content", "记忆内容")).
				Height(th.Space(23)).AutoFocus()
			ui.Row(c).Justify(ui.End).Gap(th.Space(2)).
				Padding(th.Space(2), 0, 0, 0).Children(func() {
				if ui.Button(c, a.memoryText("Cancel", "取消")).Clicked() {
					panel.editOpen = false
				}
				if ui.PrimaryButton(c, a.memoryText("Save", "保存")).Clicked() {
					a.confirmMemoryEdit(c, panel)
				}
			})
		})
	})
}

// confirmMemoryEdit validates and writes the edited content back.
func (a *App) confirmMemoryEdit(c *ui.Context, panel *memoryPanelState) {
	content, ok := a.validateMemoryContent(c, panel.editDraft)
	if !ok {
		return
	}
	db := a.memoryStore()
	if db == nil {
		panel.editOpen = false
		return
	}
	if _, err := db.UpdateMemory(panel.editID, content); err != nil {
		c.Toast(a.memoryText("Could not save the memory.", "无法保存该记忆。"))
		return
	}
	panel.editOpen = false
	c.Toast(a.memoryText("Memory updated", "记忆已更新"))
}

// validateMemoryContent trims and checks a modal draft against the store's
// limits, toasting the reason when it is refused.
func (a *App) validateMemoryContent(c *ui.Context, draft string) (string, bool) {
	content := strings.TrimSpace(draft)
	if content == "" {
		c.Toast(a.memoryText("Memory content is required.", "记忆内容不能为空。"))
		return "", false
	}
	if len(content) > store.MaxMemoryContentBytes {
		c.Toast(a.memoryText("Memory content is too long.", "记忆内容过长。"))
		return "", false
	}
	return content, true
}

// beginMemoryClear records the surface the confirmation acts on and opens the
// Clear all modal. It is never one click: the header's danger button only
// asks, and the modal's Danger button carries the irreversible clear.
func (a *App) beginMemoryClear(panel *memoryPanelState, surface memorySurface) {
	panel.clearOpen = true
	panel.clearScope = surface.Scope
	panel.clearWorkspaceID = surface.WorkspaceID
	a.closeSelectors()
}

// memoryClearDialog confirms clearing the surface's scope. It reuses the
// session/workspace delete modal pattern: cancel is focused first so Enter
// never clears by accident, Escape or a click outside dismiss without touching
// the store, and only the Danger button clears.
func (a *App) memoryClearDialog(c *ui.Context, t *design.Tokens, surface memorySurface, panel *memoryPanelState) {
	th := t.Theme()
	global := surface.Scope == store.MemoryScopeGlobal
	title := a.memoryText("Clear all workspace memories?", "清除全部工作区记忆？")
	desc := a.memoryText(
		"This deletes every memory note in this workspace. This cannot be undone.",
		"这会删除当前工作区的全部记忆笔记，此操作不可撤销。")
	if global {
		title = a.memoryText("Clear all global memories?", "清除全部全局记忆？")
		desc = a.memoryText(
			"This deletes every global memory note on this machine. This cannot be undone.",
			"这会删除本机的全部全局记忆笔记，此操作不可撤销。")
	}
	ui.Modal(c, &panel.clearOpen, func() {
		ui.Column(c).Width(th.Space(105)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, title).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, desc).MaxLines(3)
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.memoryText("Cancel", "取消")).AutoFocus().Clicked() {
					panel.clearOpen = false
				}
				if ui.Button(c, a.memoryText("Clear memories", "清除记忆")).
					TextColor(t.Danger).Clicked() {
					panel.clearOpen = false
					a.clearMemories(c, panel)
				}
			})
		})
	})
}

// clearMemories empties the confirmed scope. Other scopes and workspaces are
// untouched by the scoped store call.
func (a *App) clearMemories(c *ui.Context, panel *memoryPanelState) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	cleared, err := db.ClearMemoriesScoped(panel.clearScope, panel.clearWorkspaceID)
	if err != nil {
		c.Toast(a.memoryText("Could not clear memories.", "无法清除记忆。"))
		return
	}
	if cleared == 0 {
		c.Toast(a.memoryText("No memories to clear", "没有可清除的记忆"))
		return
	}
	c.Toast(fmt.Sprintf(a.memoryText("Cleared %d memories", "已清除 %d 条记忆"), cleared))
}

// exportMemories writes the surface's active and pending memories as a
// markdown file under the store data directory's memory/ folder and toasts the
// path. Exporting is one-way by design: the file is not a mirror that the app
// later re-imports. "Open folder" is deliberately not implemented; the toast
// names the written file.
func (a *App) exportMemories(c *ui.Context, surface memorySurface) {
	db := a.memoryStore()
	if db == nil {
		c.Toast(a.memoryText("Memory requires the live session service.",
			"记忆功能需要运行中的会话服务。"))
		return
	}
	active, err := db.ListMemoriesScoped(surface.Scope, surface.WorkspaceID, store.MemoryStatusActive, store.MaxMemoryLimit)
	if err != nil {
		c.Toast(a.memoryText("Could not export memories.", "无法导出记忆。"))
		return
	}
	pending, err := db.ListMemoriesScoped(surface.Scope, surface.WorkspaceID, store.MemoryStatusPending, store.MaxMemoryLimit)
	if err != nil {
		c.Toast(a.memoryText("Could not export memories.", "无法导出记忆。"))
		return
	}
	now := time.Now()
	dir := filepath.Join(db.Dir(), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.Toast(a.memoryText("Could not export memories.", "无法导出记忆。"))
		return
	}
	name := fmt.Sprintf("%s-%s.md", surface.Scope, now.Format("20060102-150405"))
	path := filepath.Join(dir, name)
	var body strings.Builder
	fmt.Fprintf(&body, "# Memory — %s\n\n", a.memoryExportTitle(surface))
	fmt.Fprintf(&body, "Generated: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&body, "Active: %d · Pending: %d\n\n", len(active), len(pending))
	body.WriteString("## Active\n\n")
	for _, memory := range active {
		fmt.Fprintf(&body, "- [%s · %s] %s (updated %s)\n",
			a.memoryKindLabel(memory.Kind), a.memorySourceLabel(memory.Source),
			memoryDisplayText(memory.Content),
			memory.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	body.WriteString("\n## Pending\n\n")
	for _, memory := range pending {
		fmt.Fprintf(&body, "- [%s · %s] %s (updated %s)\n",
			a.memoryKindLabel(memory.Kind), a.memorySourceLabel(memory.Source),
			memoryDisplayText(memory.Content),
			memory.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		c.Toast(a.memoryText("Could not export memories.", "无法导出记忆。"))
		return
	}
	c.Toast(fmt.Sprintf(a.memoryText("Exported to %s", "已导出到 %s"), path))
}

// memoryExportTitle names one surface in the exported markdown.
func (a *App) memoryExportTitle(surface memorySurface) string {
	if surface.Scope == store.MemoryScopeGlobal {
		return a.memoryText("Global (this machine)", "全局（本机）")
	}
	if ws, ok := a.memoryWorkspace(); ok {
		if name := strings.TrimSpace(ws.Name); name != "" {
			return fmt.Sprintf(a.memoryText("Workspace (%s)", "工作区（%s）"), name)
		}
	}
	return a.memoryText("Workspace", "工作区")
}

// memoryTag is one small metadata chip (kind, source).
func (a *App) memoryTag(c *ui.Context, t *design.Tokens, label string) {
	th := t.Theme()
	nativeText(c, design.TypeXXS, t.LabelSecondary, label).
		Padding(0, th.Space(1.5)).Background(t.BgHover).
		Radius(design.RadiusXS).NoWrap()
}

// memoryKindLabel names the learning taxonomy entry of a memory.
func (a *App) memoryKindLabel(kind string) string {
	switch kind {
	case store.MemoryKindUser:
		return a.memoryText("User", "用户")
	case store.MemoryKindFeedback:
		return a.memoryText("Feedback", "反馈")
	case store.MemoryKindReference:
		return a.memoryText("Reference", "参考")
	default:
		return a.memoryText("Project", "项目")
	}
}

// memorySourceLabel names who wrote the memory.
func (a *App) memorySourceLabel(source string) string {
	if source == store.MemorySourceAuto {
		return a.memoryText("Auto", "自动")
	}
	return a.memoryText("Manual", "手动")
}

// memoryEmpty is the empty state for a surface without saved memories.
func (a *App) memoryEmpty(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Padding(th.Space(10), 0).AlignItems(ui.Center).
		Gap(th.Space(2)).Children(func() {
		settingsText(c, a.memoryText("No memories yet.", "还没有记忆。"),
			design.TypeBody, t.LabelPrimary)
		settingsText(c, a.memoryText(
			"The agent will save or suggest memories as you work; they appear here for review and editing.",
			"agent 会在工作过程中保存或建议记忆；它们会出现在这里供审核与编辑。"),
			design.TypeXS, t.LabelTertiary).MaxLines(2)
	})
}

// memoryButton is a small bordered action used by the memory surfaces.
func (a *App) memoryButton(c *ui.Context, t *design.Tokens, label, accessible string, danger bool, fn func()) {
	th := t.Theme()
	color := t.LabelPrimary
	border := t.BorderL3
	if danger {
		color = t.Danger
		border = t.Danger
	}
	el := ui.ButtonBase(c).Height(th.Space(8)).PaddingX(th.Space(3)).
		Radius(design.RadiusSM).Border(1, border).AlignItems(ui.Center).
		Label(accessible).Tooltip(label).Role(ui.RoleButton).Focusable()
	if el.Hovered() || el.Focused() {
		el = el.Background(t.BgHover)
	}
	el.Children(func() {
		settingsText(c, label, design.TypeXS, color).NoWrap()
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

// memoryWorkspace is the selected workspace, ok=false when there is none.
func (a *App) memoryWorkspace() (store.Workspace, bool) {
	if a.Ctrl == nil {
		return store.Workspace{}, false
	}
	ws, ok, err := a.Ctrl.CurrentWorkspace(context.Background())
	if err != nil || !ok {
		return store.Workspace{}, false
	}
	return ws, true
}

// acceptMemory promotes one pending suggestion to active.
func (a *App) acceptMemory(c *ui.Context, id string) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	if _, err := db.SetMemoryStatus(id, store.MemoryStatusActive); err != nil {
		c.Toast(a.memoryText("Could not accept the suggestion.", "无法采纳该建议。"))
		return
	}
	c.Toast(a.memoryText("Suggestion accepted", "建议已采纳"))
}

// discardMemory deletes one pending suggestion.
func (a *App) discardMemory(c *ui.Context, id string) {
	if a.deleteMemoryByID(c, id) {
		c.Toast(a.memoryText("Suggestion discarded", "建议已丢弃"))
	}
}

// deleteMemory deletes one saved note.
func (a *App) deleteMemory(c *ui.Context, id string) {
	if a.deleteMemoryByID(c, id) {
		c.Toast(a.memoryText("Memory deleted", "记忆已删除"))
	}
}

// deleteMemoryByID removes one row and reports whether it succeeded.
func (a *App) deleteMemoryByID(c *ui.Context, id string) bool {
	db := a.memoryStore()
	if db == nil {
		return false
	}
	if err := db.DeleteMemory(id); err != nil {
		c.Toast(a.memoryText("Could not delete the memory.", "无法删除该记忆。"))
		return false
	}
	return true
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
