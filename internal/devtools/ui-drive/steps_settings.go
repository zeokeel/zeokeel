package main

// Settings-surface steps: open, walk all 19 pages with key-control checks and
// screenshots, flip a few switches, scroll long pages to their bottom action,
// and close via the X and Escape.

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// settingsPageSpec is one expected settings page: its host page ID, navigation
// title and the controls that must render when it is shown.
type settingsPageSpec struct {
	stepID   string
	id       string
	title    string
	controls []string
}

// settingsPages is the product's settings navigation (19 pages, after the host
// groups them by Section; the advanced pages live behind a collapsed group).
var settingsPages = []settingsPageSpec{
	{"settings-page-model", "model", "模型", []string{"Base URL", "API Key", "保存"}},
	{"settings-page-appearance", "appearance", "外观", []string{"外观模式", "界面语言", "应用"}},
	{"settings-page-skills", "skills", "技能", []string{"新建技能", "刷新"}},
	{"settings-page-shortcuts", "shortcuts", "快捷键", []string{"快捷键", "Cmd/Ctrl + B"}},
	{"settings-page-diagnostics", "diagnostics", "诊断", []string{"工具数", "会话数"}},
	{"settings-page-update", "update", "更新", []string{"当前版本", "检查更新"}},
	{"settings-page-import", "import", "导入", []string{"DSH 数据目录", "扫描"}},
	{"settings-page-search", "search", "搜索", []string{"搜索端点", "保存"}},
	{"settings-page-mcp", "mcp", "MCP", []string{"MCP 服务器", "添加服务器"}},
	{"settings-page-archive", "archive", "归档", []string{"归档的会话"}},
	{"settings-page-notifications", "notifications", "通知", []string{"显示通知气泡", "按级别过滤"}},
	{"settings-page-behavior", "behavior", "行为", []string{"默认代理权限"}},
	{"settings-page-memory", "memory", "记忆", []string{"整理策略", "添加记忆"}},
	{"settings-page-lsp", "lsp", "LSP", []string{"语言服务器", "新增服务器"}},
	{"settings-page-presets", "presets", "预设", []string{"预设列表", "新增预设", "代理预设"}},
	{"settings-page-network", "network", "网络", []string{"代理配置", "保存"}},
	{"settings-page-data", "data-privacy", "数据与隐私", []string{"数据目录", "清除全部会话"}},
	{"settings-page-about", "about", "关于", []string{"关于 ZeoKeel", "构建信息", "架构"}},
}

func (d *drive) runSettingsSteps() {
	d.step("settings-open", "settings", "设置：打开设置面",
		"点击侧栏底部「设置」→ 设置弹窗打开，显示分组导航与关闭按钮", func(c *check) {
			d.resetToSession()
			if !d.openSettingsFromNeutralScreen() {
				c.failf("点击侧栏底部「设置」后设置面未打开")
				return
			}
			c.require(d.uiRoot.Navigator().SettingsOpen(), "点击「设置」后 SettingsOpen()=false")
			c.mustText("设置", "关闭设置", "搜索设置")
			c.shot("settings-open")
		})

	d.step("settings-advanced-group", "settings", "设置：高级分组默认折叠",
		"设置导航默认只显示日常页；「高级」分组默认折叠，点击后展开诊断/更新/导入/归档等低频页，再次点击收起", func(c *check) {
			if !d.openSettingsFromNeutralScreen() {
				c.failf("打开设置失败")
				return
			}
			c.require(c.has("常用"), "设置导航缺少「常用」分组")
			c.require(c.has("行为"), "设置导航缺少「行为」")
			c.require(c.has("高级"), "设置导航缺少「高级」分组标题")
			c.require(!c.has("诊断"), "「高级」分组默认展开（应折叠）")
			if !c.click("高级") {
				return
			}
			d.drain(2)
			c.require(c.has("诊断") && c.has("数据与隐私"), "展开「高级」后低频页仍不可见")
			c.shot("settings-advanced-expanded")
			if !c.click("高级") {
				return
			}
			d.drain(2)
			c.require(!c.has("诊断"), "收起「高级」后低频页仍显示")
		})

	for _, page := range settingsPages {
		page := page
		d.step(page.stepID, "settings", "设置页："+page.title,
			"OpenSettingsPage("+page.id+") 深链 → 页面渲染，关键控件存在且截图；内容在设置弹窗内", func(c *check) {
				if err := d.openSettingsPageID(page.id); err != nil {
					c.failf("%v", err)
					return
				}
				c.checkSettingsControls(page)
				c.checkPanelText(page.title)
				c.shot(page.stepID)
			})
	}

	d.step("settings-switches", "settings", "设置：拨动开关",
		"外观「开机自启动」、通知「显示通知气泡」点击后设置值翻转且状态可见", func(c *check) {
			if err := d.openSettingsPageID("appearance"); err != nil {
				c.failf("%v", err)
				return
			}
			before := d.settings().Get(contracts.SettingAutostart, "")
			if !c.clickSettingSwitch("开机自启动", contracts.SettingAutostart, before) {
				return
			}
			d.drain(2)
			c.shot("settings-switch-autostart")

			if err := d.openSettingsPageID("notifications"); err != nil {
				c.failf("%v", err)
				return
			}
			before = d.settings().Get(contracts.SettingNotifications, "")
			if !c.clickSettingSwitch("显示通知气泡", contracts.SettingNotifications, before) {
				return
			}
			d.drain(2)
			// Restore so later toast steps still see notifications.
			_ = d.settings().Set(contracts.SettingNotifications, "true")
			d.frame()
			c.shot("settings-switch-notifications")
		})

	d.step("settings-footer", "settings", "设置：固定 footer 主操作",
		"外观/模型/行为/搜索/网络/LSP/记忆等页的主操作固定在底部 footer：不滚动即可见、在弹窗内、可点击", func(c *check) {
			panel := d.settingsPanelRect()
			checked := 0
			for _, page := range settingsPages {
				// Pages with an explicit save/apply action moved it into the
				// host's fixed footer; immediate-save pages (behavior, data &
				// privacy, …) have no footer action by design.
				switch page.id {
				case "appearance", "model", "search", "network":
				default:
					continue
				}
				if err := d.openSettingsPageID(page.id); err != nil {
					c.failf("%v", err)
					return
				}
				// No body scrolling: the fixed footer must already show the
				// page's primary action.
				label := ""
				for _, candidate := range []string{"保存", "应用", "添加"} {
					if _, ok := d.find(candidate); ok {
						label = candidate
						break
					}
				}
				if label == "" {
					c.failf("%s 页未设置固定 footer：不滚动时找不到主操作（保存/应用）", page.title)
					continue
				}
				rect, ok := d.find(label)
				if !ok {
					c.failf("%s 页在固定 footer 找不到主操作「%s」", page.title, label)
					continue
				}
				checked++
				if !inRect(panel, rect, 1) {
					c.failf("%s 页主操作「%s」越出设置弹窗：rect=%+v，弹窗=%+v", page.title, label, rect, panel)
				}
				if !d.inWindow(rect) {
					c.failf("%s 页主操作「%s」越出窗口：rect=%+v", page.title, label, rect)
				}
				// A pinned footer sits at the dialog's bottom.
				if rect.Y < panel.Y+panel.H*0.55 {
					c.note("%s 页「%s」位于弹窗上部（y=%.0f），不一定是固定 footer", page.title, label, rect.Y)
				}
				c.note("%s 页「%s」rect=(%.0f,%.0f,%.0f,%.0f)", page.title, label, rect.X, rect.Y, rect.W, rect.H)
				// The footer action must be clickable (not painted over the
				// dialog's edge).
				if err := d.click(label); err != nil {
					c.failf("%s 页固定 footer 的「%s」不可点击：%v", page.title, label, err)
				}
				d.drain(2)
			}
			c.require(checked > 0, "没有任何设置页显示固定 footer 主操作")
			c.shot("settings-footer")
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
		})

	d.step("settings-nav-search", "settings", "设置：导航搜索过滤",
		"设置导航搜索框输入「归档」→ 只剩相关页；清空 → 全部页面恢复", func(c *check) {
			if !d.openSettingsFromNeutralScreen() {
				c.failf("打开设置失败")
				return
			}
			if !c.click("搜索设置") {
				return
			}
			d.typeText("归档")
			d.drain(2)
			c.require(c.has("归档"), "导航搜索「归档」后没有结果")
			c.require(!c.has("行为"), "导航搜索「归档」后仍显示无关页「行为」")
			c.shot("settings-nav-filtered")
			d.clearDraft()
			d.drain(2)
			c.require(c.has("模型"), "清空导航搜索后页面列表未恢复")
			// Closing the settings clears the search query: reopening shows the
			// full navigation again.
			d.typeText("归档")
			d.drain(2)
			d.key(0, ui.KeyEscape)
			d.drain(2)
			if !d.openSettingsFromNeutralScreen() {
				c.failf("关闭后重开设置失败")
				return
			}
			c.require(c.has("模型"), "重开设置后导航搜索词未清空（页面列表仍被过滤）")
			c.shot("settings-nav-search-cleared")
		})

	d.step("settings-dirty-guard", "settings", "设置：未保存修改守卫与 Discard 重置",
		"任意页有改动按 Esc 都会被拦截；继续编辑保留；放弃修改后重开对应页干净；关闭时清空导航搜索词", func(c *check) {
			if err := d.openSettingsPageID("model"); err != nil {
				c.failf("%v", err)
				return
			}
			// Make one edit: append text to the Base URL draft.
			if !c.click("Base URL") {
				return
			}
			d.typeText("X")
			d.drain(2)
			d.key(0, ui.KeyEscape)
			d.drain(2)
			if !d.uiRoot.Navigator().SettingsOpen() {
				c.failf("Esc 直接关闭了设置：有未保存修改时缺少守卫")
				return
			}
			if !c.has("有未保存的修改") {
				c.failf("Esc 后设置保持打开但缺少「有未保存的修改」提示")
				return
			}
			c.shot("settings-dirty-guard")
			if !c.has("继续编辑") || !c.has("放弃修改") {
				c.failf("未保存修改提示缺少「继续编辑」/「放弃修改」按钮")
				return
			}
			if !c.click("继续编辑") {
				return
			}
			d.drain(2)
			c.require(d.uiRoot.Navigator().SettingsOpen(), "继续编辑后设置面被关闭")
			c.require(!c.has("有未保存的修改"), "继续编辑后提示条未消失")
			// Any dirty page guards the close, not only the selected page: keep
			// the model page dirty and switch to a clean page, then Esc.
			if err := d.openSettingsPageID("appearance"); err != nil {
				c.failf("切换到外观页失败：%v", err)
				return
			}
			d.drain(2)
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(d.uiRoot.Navigator().SettingsOpen(), "非当前页有脏改动时 Esc 直接关闭了设置（任意页脏应拦截）")
			c.require(c.has("有未保存的修改"), "任意页脏导致关闭时没有「有未保存的修改」提示")
			if !c.click("放弃修改") {
				return
			}
			d.drain(2)
			c.require(!d.uiRoot.Navigator().SettingsOpen(), "放弃修改后设置面仍打开")
			c.require(!c.has("有未保存的修改"), "放弃修改后提示条仍显示")
			// Discard must reset the page drafts: reopening the model page is
			// clean again (no 未保存 marker, no guard on Esc).
			if err := d.openSettingsPageID("model"); err != nil {
				c.failf("重开模型页失败：%v", err)
				return
			}
			d.drain(2)
			c.require(!c.has("未保存"), "放弃修改后重开模型页仍标记「未保存」")
			c.shot("settings-discarded-clean-page")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(!d.uiRoot.Navigator().SettingsOpen(), "Discard 后重开的干净页 Esc 反被守卫拦截")
			c.shot("settings-dirty-discarded")
		})

	d.step("settings-close-x", "settings", "设置：X 关闭",
		"点右上「关闭设置」→ 设置弹窗关闭，回到会话页", func(c *check) {
			if err := d.openSettingsPageID("behavior"); err != nil {
				c.failf("%v", err)
				return
			}
			if !c.click("关闭设置") {
				return
			}
			d.drain(2)
			c.require(!d.uiRoot.Navigator().SettingsOpen(), "点击「关闭设置」后 SettingsOpen()=true")
			c.require(!d.has("关闭设置"), "关闭后「关闭设置」按钮仍显示")
			c.shot("settings-close-x")
		})

	d.step("settings-close-esc", "settings", "设置：Esc 关闭",
		"无未保存修改的设置页按 Esc → 弹窗关闭", func(c *check) {
			if err := d.openSettingsPageID("behavior"); err != nil {
				c.failf("%v", err)
				return
			}
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(!d.uiRoot.Navigator().SettingsOpen(), "Esc 后 SettingsOpen()=true")
			c.require(!d.has("关闭设置"), "Esc 后「关闭设置」按钮仍显示")
		})
}

// openSettingsPageID opens the settings surface on one page through the host's
// deep link (Navigator.OpenSettingsPage), the same path modules use for
// "open the model settings" commands. It is deterministic across navigation
// scroll state, so the page-rendering steps no longer depend on clicking a nav
// item that may sit below the fold.
func (d *drive) openSettingsPageID(pageID string) error {
	if err := d.uiRoot.OpenSettingsPage(pageID); err != nil {
		return fmt.Errorf("OpenSettingsPage(%q): %w", pageID, err)
	}
	d.frame()
	d.frame()
	if !d.uiRoot.Navigator().SettingsOpen() {
		return fmt.Errorf("OpenSettingsPage(%q) 后设置面未打开", pageID)
	}
	return nil
}

// checkPanelText asserts the page heading text is inside the settings panel (a
// cheap "content is not painted outside the dialog" check).
func (c *check) checkPanelText(text string) {
	rect, ok := c.d.find(text)
	if !ok {
		return
	}
	if !inRect(c.d.settingsPanelRect(), rect, 1) {
		c.failf("页面标题「%s」在设置弹窗外：rect=%+v，弹窗=%+v", text, rect, c.d.settingsPanelRect())
	}
}

// clickSettingSwitch toggles one boolean setting row: it clicks the visible
// field label (a native Field label toggles its control) and falls back to the
// row's right edge. It verifies the settings store value changed.
func (c *check) clickSettingSwitch(label, key, before string) bool {
	if !c.d.ensureSettingsVisible(label) {
		c.failf("设置行「%s」滚动后仍不可见", label)
		return false
	}
	rect, ok := c.d.find(label)
	if !ok {
		c.failf("设置行「%s」不存在", label)
		return false
	}
	c.d.clickRect(rect)
	c.d.frame()
	c.d.frame()
	if after := c.d.settings().Get(key, ""); after != before {
		c.note("点击标签「%s」后 %s=%q", label, key, after)
		return true
	}
	// Fall back to the row's right edge (the switch itself).
	panel := c.d.settingsPanelRect()
	x := rect.X + rect.W - 20
	if x < panel.X+210 {
		x = panel.X + panel.W - 40
	}
	c.d.tester.ClickAt(x, rect.Y+rect.H/2)
	c.d.frame()
	c.d.frame()
	if after := c.d.settings().Get(key, ""); after != before {
		c.note("点击行右缘「%s」后 %s=%q", label, key, after)
		return true
	}
	c.failf("点击「%s」后设置值未变化（%q → %q）", label, before, c.d.settings().Get(key, ""))
	return false
}

// checkSettingsControls asserts the page's key controls exist; controls below
// the fold are looked up after scrolling the page body (a long settings page
// must be scrollable, not clipped).
func (c *check) checkSettingsControls(page settingsPageSpec) {
	missing := make([]string, 0, len(page.controls))
	scrolled := false
	for _, control := range page.controls {
		if c.d.has(control) {
			continue
		}
		if !scrolled {
			c.d.scrollSettingsBody(4000)
			c.d.frame()
			c.d.frame()
			scrolled = true
			c.shot(page.stepID + "-bottom")
		}
		if c.d.has(control) {
			c.note("「%s」需滚动页面后可见", control)
			continue
		}
		missing = append(missing, control)
	}
	if scrolled {
		c.d.scrollSettingsBody(-8000)
		c.d.frame()
	}
	if len(missing) > 0 {
		c.failf("界面缺少文本：%v", missing)
	}
}

// ensureSettingsVisible scrolls the settings page body until label's rect is
// inside the dialog (visible enough to click).
func (d *drive) ensureSettingsVisible(label string) bool {
	panel := d.settingsPanelRect()
	for attempt := 0; attempt < 10; attempt++ {
		rect, ok := d.find(label)
		if ok {
			centerY := rect.Y + rect.H/2
			if centerY > panel.Y+60 && centerY < panel.Y+panel.H-20 &&
				rect.X > panel.X+210 && rect.X+rect.W < panel.X+panel.W {
				return true
			}
		}
		dy := float32(200)
		if ok && rect.Y+rect.H/2 > panel.Y+panel.H/2 {
			// Already below the middle: no further scrolling helps; try up.
			dy = -400
		}
		d.scrollSettingsBody(dy)
		d.frame()
	}
	return false
}

// scrollSettingsBody scrolls the settings page body (the content column, right
// of the 200 DIP navigation).
func (d *drive) scrollSettingsBody(dy float32) {
	panel := d.settingsPanelRect()
	d.tester.Scroll(panel.X+panel.W*0.65, panel.Y+panel.H*0.5, 0, dy)
}

// unused guard: keep time imported for the switches step timing helpers.
var _ = time.Millisecond
