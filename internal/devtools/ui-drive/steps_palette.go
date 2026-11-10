package main

// Command-palette steps: Cmd+K opens the native palette (SearchField + List);
// filtering, keyboard choice and Enter must run the command, Esc must close it.

import (
	"github.com/egoist/mygo/ui"
)

// openPalette resets to the session screen, opens the command palette and
// reports whether it appeared.
func (d *drive) openPalette() bool {
	d.resetToSession()
	d.key(ui.Cmd, ui.KeyK)
	d.drain(2)
	return d.has("命令面板") && d.has("搜索命令")
}

// focusPaletteSearch clicks the palette search field so typing filters the
// command list (the palette has no autofocus).
func (d *drive) focusPaletteSearch() bool {
	if _, ok := d.find("搜索命令"); !ok {
		return false
	}
	if err := d.click("搜索命令"); err != nil {
		return false
	}
	d.frame()
	return true
}

func (d *drive) runPaletteSteps() {
	d.step("palette-open", "uiroot", "命令面板：Cmd+K 打开",
		"Cmd+K → 命令面板出现（标题/搜索框/命令行/底部提示）", func(c *check) {
			c.require(d.openPalette(), "Cmd+K 后命令面板未出现")
			c.shot("palette-open")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(!d.has("命令面板"), "Esc 后命令面板仍显示")
		})

	d.step("palette-shortcut-column", "uiroot", "命令面板：快捷键列",
		"过滤「切换右侧栏」→ 该命令行显示快捷键 chord（Cmd/Ctrl + Alt + I）", func(c *check) {
			if !d.openPalette() {
				c.failf("命令面板未打开")
				return
			}
			if !d.focusPaletteSearch() {
				c.failf("命令面板没有可点击的搜索框")
				return
			}
			d.typeText("切换右侧栏")
			d.drain(2)
			c.require(c.has("切换右侧栏"), "过滤后没有「切换右侧栏」命令")
			c.require(c.has("Cmd/Ctrl + Alt + I") || c.has("Cmd/Ctrl + Alt"),
				"命令面板行尾没有显示快捷键 chord；texts=%v", paletteTailTexts(d))
			// The right sidebar's page commands render their chords too: the
			// retired pane-switch commands are gone with the 8-pane inspector.
			d.clearDraft()
			d.typeText("打开文件页")
			d.drain(2)
			c.require(c.has("Ctrl+Alt+P"),
				"命令面板缺少文件页快捷键 chord；texts=%v", paletteTailTexts(d))
			d.clearDraft()
			d.typeText("打开命令页")
			d.drain(2)
			c.require(c.has("Ctrl+`"),
				"命令面板缺少命令页快捷键 chord；texts=%v", paletteTailTexts(d))
			c.shot("palette-shortcut-column")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
		})

	d.step("palette-filter-enter", "uiroot", "命令面板：输入过滤 + Enter 执行",
		"点击搜索框输入「设置」过滤 → Enter 运行首行命令（打开设置）", func(c *check) {
			if !d.openPalette() {
				c.failf("命令面板未打开")
				return
			}
			if !d.focusPaletteSearch() {
				c.failf("命令面板没有可点击的搜索框")
				return
			}
			d.typeText("设置")
			d.drain(2)
			c.require(c.has("设置"), "输入「设置」后过滤结果里没有设置命令")
			c.require(!c.has("新会话"), "输入「设置」后未过滤（「新会话」仍在列表）")
			c.shot("palette-filtered")
			d.key(0, ui.KeyEnter)
			d.drain(3)
			c.require(!c.has("搜索命令"), "Enter 后命令面板未关闭")
			c.require(d.uiRoot.Navigator().SettingsOpen(), "Enter 运行首行命令后设置未打开")
			c.shot("palette-enter-settings")
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
		})

	d.step("palette-down-enter", "uiroot", "命令面板：↓ 选择 + Enter 执行",
		"搜索过滤「设置」→ ↓ 移动到非首行命令 → Enter 打开目标对话框（首行「设置」只打开设置面）", func(c *check) {
			// The number of rows matching 设置 depends on how many modules
			// register that command title (the palette dedupes equal titles),
			// so try one, two and three Down presses until the chosen row is
			// the goal command (设置目标) rather than the first row (设置).
			try := func(downs int) bool {
				if !d.openPalette() {
					return false
				}
				if !d.focusPaletteSearch() {
					return false
				}
				d.typeText("设置")
				d.drain(2)
				if !c.has("设置") {
					return false
				}
				for index := 0; index < downs; index++ {
					d.key(0, ui.KeyDown)
					d.frame()
				}
				d.key(0, ui.KeyEnter)
				d.drain(3)
				goalDialog := d.has("目标内容")
				// Clean up whatever the attempt opened.
				if d.has("搜索命令") {
					d.key(0, ui.KeyEscape)
					d.drain(2)
				}
				if goalDialog {
					return true
				}
				if d.uiRoot.Navigator().SettingsOpen() {
					d.uiRoot.Navigator().CloseSettings()
					d.frame()
				}
				return false
			}
			for downs := 1; downs <= 3; downs++ {
				if try(downs) {
					c.note("↓ 在搜索框内生效：%d 次 Down 后 Enter 运行了「设置目标」", downs)
					c.shot("palette-down-goal")
					_ = d.click("取消")
					d.frame()
					return
				}
			}
			c.failf("↓↓+Enter 未能选中「设置目标」（每次尝试都运行了首行或未执行）")
		})

	d.step("palette-tab-enter", "uiroot", "命令面板：Tab→Enter 路径",
		"Cmd+K → 输入「新」过滤 → Tab 把焦点移到列表 → Enter 运行「新会话」命令", func(c *check) {
			if !d.openPalette() {
				c.failf("命令面板未打开")
				return
			}
			sessionsBefore := len(d.chat().Sessions())
			d.typeText("新")
			d.drain(2)
			c.require(c.has("新会话") && !c.has("导出对话"), "输入「新」后未过滤出「新会话」")
			d.key(0, ui.KeyTab)
			d.frame()
			d.key(0, ui.KeyEnter)
			d.drain(3)
			if c.has("搜索命令") {
				c.warnf("Tab→Enter 未关闭命令面板（Enter 可能未提交选择）")
				d.key(0, ui.KeyEscape)
				d.drain(2)
				d.clearDraft()
				return
			}
			sessionsAfter := len(d.chat().Sessions())
			c.require(sessionsAfter > sessionsBefore,
				"Tab→Enter 未运行「新会话」命令（会话数 %d → %d）", sessionsBefore, sessionsAfter)
			c.shot("palette-tab-enter")
			d.restoreOriginalSession()
			d.clearDraft()
		})

	d.step("palette-immediate-type", "uiroot", "命令面板：打开后直接输入",
		"Cmd+K → 不点击直接输入「设置」→ 搜索框收到输入并过滤（键盘优先）", func(c *check) {
			if !d.openPalette() {
				c.failf("命令面板未打开")
				return
			}
			d.typeText("设置")
			d.drain(2)
			filtered := c.has("设置") && !c.has("新会话")
			if !filtered {
				c.warnf("面板打开后直接输入未进入搜索框（需先点击/Tab 聚焦；首行命令列表未过滤）")
			}
			c.shot("palette-immediate-type")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
			d.restoreOriginalSession()
		})
}
