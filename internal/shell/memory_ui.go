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

// Settings › Memory (docs/design/auto-memory.md §6): the two-scope memory
// control surface. The Global tab is the machine-wide preference store shared
// by every workspace; the Workspace tab is project context bound to the
// selected workspace. Each tab owns its Off/Suggest/Auto mode in Settings, a
// pending inbox ("Suggestions to review") whose Accept/Discard decisions write
// through the store, the active list with per-row delete, scoped Clear all and
// a markdown export. The list is read from the store on every frame, so it is
// never a stale cache; the page is driven through the same live session
// service the rest of Settings uses.
const settingsTabMemory settingsTab = settingsTabAbout + 1

// memoryIcon is the nav cell's note glyph, drawn to the same 16x16 line-icon
// geometry as the other settings icons.
var memoryIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round"><path d="M3.2 2.4h9.6v11.2H3.2z"/><path d="M5.4 5.4h5.2M5.4 8h5.2M5.4 10.6h3.2"/></svg>`))

// memoryTab indexes the page's two scope tabs.
type memoryTab int

const (
	memoryTabGlobal memoryTab = iota
	memoryTabWorkspace
)

// memoryModeValues is the ordered Off/Suggest/Auto mode catalog. The values are
// the persisted setting values (session.MemoryMode*).
var memoryModeValues = []string{
	session.MemoryModeOff,
	session.MemoryModeSuggest,
	session.MemoryModeAuto,
}

// memoryPageState is the App-scoped interaction state of the page: the
// selected tab and the scope a pending Clear all confirmation acts on. It is
// kept per App (like the shell's other view state) so switching tabs and
// leaving the page keeps the selection, without adding fields to App.
type memoryPageState struct {
	tab              int
	clearScope       string
	clearWorkspaceID string
}

var memoryPageStates sync.Map // map[*App]*memoryPageState

// memoryPageStateFor returns the page state of a, making it on first use. The
// Workspace tab is the default: it is the scope the old page showed and the one
// most turns touch.
func (a *App) memoryPageStateFor() *memoryPageState {
	if raw, ok := memoryPageStates.Load(a); ok {
		return raw.(*memoryPageState)
	}
	st := &memoryPageState{tab: int(memoryTabWorkspace)}
	actual, _ := memoryPageStates.LoadOrStore(a, st)
	return actual.(*memoryPageState)
}

// openMemorySettings opens Settings on the Memory page, the target of the
// /memory command and of the page's other entry points.
func (a *App) openMemorySettings() {
	a.shellUI().settingsTab = settingsTabMemory
	a.openSettings()
}

// memorySettingsPage is Settings › Memory.
func (a *App) memorySettingsPage(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.memoryPageStateFor()
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		ui.Column(c).Gap(th.Space(1)).Children(func() {
			settingsText(c, a.memoryText("Memory", "记忆"), design.TypeL, t.LabelPrimary).
				FontWeight(600)
			settingsText(c, a.memoryText(
				"Persistent notes the agent injects into future turns. Workspace memories stay in this workspace; global memories apply to every session on this machine.",
				"agent 会注入未来回合的持久笔记。工作区记忆仅限当前工作区；全局记忆适用于本机的所有会话。"),
				design.TypeXS, t.LabelTertiary).MaxLines(3)
		})
		a.memoryScopeTabs(c, t, st)
		scope, workspaceID, workspaceOK := a.memoryScope(st)
		if scope == store.MemoryScopeWorkspace && !workspaceOK {
			settingsText(c, a.memoryText(
				"Select a workspace to view its memories.",
				"请选择工作区以查看其记忆。"), design.TypeXS, t.LabelTertiary)
			return
		}
		a.memoryModeRow(c, t, scope)
		a.memorySummaryRow(c, t, st, scope, workspaceID)
		a.memoryPendingSection(c, t, scope, workspaceID)
		a.memoryActiveSection(c, t, scope, workspaceID)
	})
	a.memoryClearDialog(c, t)
}

// memoryScope resolves the current tab to its store scope. A workspace tab
// without a selected workspace reports ok=false so the page can ask for one.
func (a *App) memoryScope(st *memoryPageState) (scope, workspaceID string, ok bool) {
	if st.tab == int(memoryTabGlobal) {
		return store.MemoryScopeGlobal, "", true
	}
	ws, found := a.memoryWorkspace()
	if !found {
		return store.MemoryScopeWorkspace, "", false
	}
	return store.MemoryScopeWorkspace, ws.ID, true
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

// memoryScopeTabs draws the Global/Workspace tabs. TabsBase owns the tab role,
// the checked state, the click selection and the Left/Right/Home/End keys, so
// the page does not reimplement native tab semantics.
func (a *App) memoryScopeTabs(c *ui.Context, t *design.Tokens, st *memoryPageState) {
	th := t.Theme()
	globalLabel := a.memoryText("Global (this machine)", "全局（本机）")
	workspaceLabel := a.memoryText("Workspace", "工作区")
	if ws, ok := a.memoryWorkspace(); ok {
		if name := strings.TrimSpace(ws.Name); name != "" {
			workspaceLabel = fmt.Sprintf(a.memoryText("Workspace (%s)", "工作区（%s）"), name)
		}
	}
	tabs := ui.TabsBase(c, &st.tab, 2)
	tabs.List.Height(th.Space(9)).Gap(th.Space(2)).AlignItems(ui.Stretch).Children(func() {
		a.memoryScopeTab(c, t, tabs, int(memoryTabGlobal), globalLabel)
		a.memoryScopeTab(c, t, tabs, int(memoryTabWorkspace), workspaceLabel)
	})
}

// memoryScopeTab is one tab of the scope list: selected tabs lift onto the
// panel surface with the accent underline treatment the session tabs use.
func (a *App) memoryScopeTab(c *ui.Context, t *design.Tokens, tabs ui.TabsParts, index int, label string) {
	th := t.Theme()
	active := a.memoryPageStateFor().tab == index
	tab := tabs.Tab(index).Height(th.Space(9)).PaddingX(th.Space(3)).
		Radius(design.RadiusSM).AlignItems(ui.Center).Label(label)
	switch {
	case active:
		tab = tab.Background(t.BgStrong).Border(1, t.BorderL3)
	case tab.Hovered():
		tab = tab.Background(t.BgHover)
	}
	tab.Children(func() {
		color, weight := t.LabelTertiary, 400
		if active {
			color, weight = t.LabelPrimary, 600
		}
		nativeText(c, design.TypeXS, color, label).FontWeight(weight).NoWrap()
	})
}

// memoryModeRow is the tab's Off/Suggest/Auto mode control and its explanation.
// The segmented-looking radio group writes the scope's own Settings field, so
// changing one scope never changes the other.
func (a *App) memoryModeRow(c *ui.Context, t *design.Tokens, scope string) {
	th := t.Theme()
	mode := a.memoryMode(scope)
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
				button.OnClick(func() { a.setMemoryMode(c, scope, value) })
			}
		})
		settingsText(c, a.memoryModeDescription(mode), design.TypeXS, t.LabelTertiary)
	})
}

// memoryMode resolves the persisted mode of one scope, with the design's
// defaults when Settings has not stored it yet (workspace auto, global suggest).
func (a *App) memoryMode(scope string) string {
	cfg := a.currentSettings()
	mode := cfg.MemoryWorkspaceMode
	if scope == store.MemoryScopeGlobal {
		mode = cfg.MemoryGlobalMode
	}
	switch mode {
	case session.MemoryModeOff, session.MemoryModeSuggest, session.MemoryModeAuto:
		return mode
	}
	if scope == store.MemoryScopeGlobal {
		return session.MemoryModeSuggest
	}
	return session.MemoryModeAuto
}

// setMemoryMode persists one scope's mode through the Settings store.
func (a *App) setMemoryMode(c *ui.Context, scope, mode string) {
	if a.memoryMode(scope) == mode {
		return
	}
	a.updateSettings(c, func(cfg *session.Settings) {
		if scope == store.MemoryScopeGlobal {
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

// memorySummaryRow is the counts line plus the two destructive/bulk actions.
func (a *App) memorySummaryRow(c *ui.Context, t *design.Tokens, st *memoryPageState, scope, workspaceID string) {
	th := t.Theme()
	active, pending := 0, 0
	if db := a.memoryStore(); db != nil {
		var err error
		active, pending, err = db.MemoryCounts(scope, workspaceID)
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
			a.memoryButton(c, t, a.memoryText("Clear all", "全部清除"),
				a.memoryText("Clear all memories", "清除全部记忆"), true, func() {
					a.beginMemoryClear(st, scope, workspaceID)
				})
			a.memoryButton(c, t, a.memoryText("Export markdown", "导出 Markdown"),
				a.memoryText("Export markdown", "导出 Markdown"), false, func() {
					a.exportMemories(c, scope, workspaceID)
				})
		})
	})
}

// memoryPendingSection is the pending inbox: the suggestions the suggest mode
// left for review. Each row offers Accept (promote to active) and Discard
// (delete); an empty inbox draws nothing.
func (a *App) memoryPendingSection(c *ui.Context, t *design.Tokens, scope, workspaceID string) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	pending, err := db.ListMemoriesScoped(scope, workspaceID, store.MemoryStatusPending, store.MaxMemoryLimit)
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
				a.memoryPendingRow(c, t, scope, memory, i == len(pending)-1)
			}
		})
	})
}

// memoryPendingRow is one pending suggestion: content and scope/kind tags on
// the left, Accept and Discard on the right. The action labels carry the
// memory id so a list with several suggestions stays unambiguous to assistive
// technology and tests.
func (a *App) memoryPendingRow(c *ui.Context, t *design.Tokens, scope string, memory store.Memory, last bool) {
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
				a.memoryTag(c, t, a.memoryScopeLabel(scope))
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

// memoryActiveSection is the saved-memory list of the tab, or the empty state.
func (a *App) memoryActiveSection(c *ui.Context, t *design.Tokens, scope, workspaceID string) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	active, err := db.ListMemoriesScoped(scope, workspaceID, store.MemoryStatusActive, store.MaxMemoryLimit)
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
				a.memoryActiveRow(c, t, memory, i == len(active)-1)
			}
		})
	})
}

// memoryActiveRow is one saved note: content and metadata on the left, Delete
// on the right.
func (a *App) memoryActiveRow(c *ui.Context, t *design.Tokens, memory store.Memory, last bool) {
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
		a.memoryButton(c, t, a.memoryText("Delete", "删除"),
			a.memoryText("Delete memory ", "删除记忆 ")+id, true, func() {
				a.deleteMemory(c, id)
			})
	})
}

// memoryTag is one small metadata chip (scope, kind, source).
func (a *App) memoryTag(c *ui.Context, t *design.Tokens, label string) {
	th := t.Theme()
	nativeText(c, design.TypeXXS, t.LabelSecondary, label).
		Padding(0, th.Space(1.5)).Background(t.BgHover).
		Radius(design.RadiusXS).NoWrap()
}

// memoryScopeLabel names one scope in a tag and in the export title.
func (a *App) memoryScopeLabel(scope string) string {
	if scope == store.MemoryScopeGlobal {
		return a.memoryText("Global (this machine)", "全局（本机）")
	}
	return a.memoryText("Workspace", "工作区")
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

// memoryEmpty is the empty state for a tab without saved memories.
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

// memoryButton is a small bordered action used by the Memory page.
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

// beginMemoryClear records the scope the confirmation acts on and opens the
// Clear all modal. It is never one click: the header's danger button only
// asks, and the modal's Danger button carries the irreversible clear.
func (a *App) beginMemoryClear(st *memoryPageState, scope, workspaceID string) {
	st.clearScope = scope
	st.clearWorkspaceID = workspaceID
	a.memoryClearOpen = true
	a.closeSelectors()
}

// memoryClearDialog confirms clearing the current tab's scope. It reuses the
// session/workspace delete modal pattern: cancel is focused first so Enter
// never clears by accident, Escape or a click outside dismiss without touching
// the store, and only the Danger button clears.
func (a *App) memoryClearDialog(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.memoryPageStateFor()
	global := st.clearScope == store.MemoryScopeGlobal
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
	ui.Modal(c, &a.memoryClearOpen, func() {
		ui.Column(c).Width(th.Space(105)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, title).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, desc).MaxLines(3)
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.memoryText("Cancel", "取消")).AutoFocus().Clicked() {
					a.memoryClearOpen = false
				}
				if ui.Button(c, a.memoryText("Clear memories", "清除记忆")).
					TextColor(t.Danger).Clicked() {
					a.memoryClearOpen = false
					a.clearMemories(c)
				}
			})
		})
	})
}

// clearMemories empties the confirmed scope. Other scopes and workspaces are
// untouched by the scoped store call.
func (a *App) clearMemories(c *ui.Context) {
	db := a.memoryStore()
	if db == nil {
		return
	}
	st := a.memoryPageStateFor()
	cleared, err := db.ClearMemoriesScoped(st.clearScope, st.clearWorkspaceID)
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

// exportMemories writes the tab's active and pending memories as a markdown
// file under the store data directory's memory/ folder and toasts the path.
// Exporting is one-way by design (docs/design/auto-memory.md §6): the file is
// not a mirror that the app later re-imports. "Open folder" is deliberately not
// implemented; the toast names the written file.
func (a *App) exportMemories(c *ui.Context, scope, workspaceID string) {
	db := a.memoryStore()
	if db == nil {
		c.Toast(a.memoryText("Memory requires the live session service.",
			"记忆功能需要运行中的会话服务。"))
		return
	}
	active, err := db.ListMemoriesScoped(scope, workspaceID, store.MemoryStatusActive, store.MaxMemoryLimit)
	if err != nil {
		c.Toast(a.memoryText("Could not export memories.", "无法导出记忆。"))
		return
	}
	pending, err := db.ListMemoriesScoped(scope, workspaceID, store.MemoryStatusPending, store.MaxMemoryLimit)
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
	name := fmt.Sprintf("%s-%s.md", scope, now.Format("20060102-150405"))
	path := filepath.Join(dir, name)
	var body strings.Builder
	fmt.Fprintf(&body, "# Memory — %s\n\n", a.memoryExportTitle(scope))
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

// memoryExportTitle names one scope in the exported markdown.
func (a *App) memoryExportTitle(scope string) string {
	if scope == store.MemoryScopeGlobal {
		return a.memoryText("Global (this machine)", "全局（本机）")
	}
	if ws, ok := a.memoryWorkspace(); ok {
		if name := strings.TrimSpace(ws.Name); name != "" {
			return fmt.Sprintf(a.memoryText("Workspace (%s)", "工作区（%s）"), name)
		}
	}
	return a.memoryText("Workspace", "工作区")
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
