package main

// Shell-level steps: navigation, theme/language, sidebar, window sizes, toasts.
// These drive the window chrome the host (uiroot) owns; failures here point at
// uiroot/shell rather than at a page module.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func (d *drive) runShellSteps() {
	// --- navigation -------------------------------------------------------
	d.step("nav-landing", "uiroot", "导航：新对话落地页",
		"侧栏「新对话」→ 进入会话页的新对话落地（hero 开始新对话 + composer），导航状态 session（旧的首页导航已移除）", func(c *check) {
			d.resetToSession()
			if !c.click("新对话") {
				return
			}
			d.drain(3)
			c.mustText("开始新对话", "输入消息…")
			c.require(d.uiRoot.Navigator().Current() == "session",
				"navigator.Current()=%q，期望 session", d.uiRoot.Navigator().Current())
			c.require(!d.hasExactText("首页"), "侧栏仍显示已移除的「首页」导航项")
			c.shot("nav-landing")
		})

	d.step("nav-automation", "uiroot", "导航：自动化",
		"侧栏点击「自动化」→ automation screen 渲染；任务行动作 立即运行/编辑/⋯ 菜单；调度显示人类可读", func(c *check) {
			if !c.click("自动化") {
				return
			}
			c.mustText("自动化", "新建任务", "每日构建检查", "立即运行", "编辑")
			// Delete is a danger action behind the row's ⋯ menu now (R1).
			if !c.click("任务操作") {
				return
			}
			d.frame()
			c.mustText("删除任务")
			d.tester.Key(0, ui.KeyEscape)
			d.frame()
			// The seeded task runs every 30 minutes: the raw cron is humanized
			// and the raw plan only stays in the tooltip (H11).
			c.require(c.has("每 30 分钟"), "自动化任务调度没有显示人类可读文案「每 30 分钟」")
			c.require(!c.has("*/30 * * * *"), "自动化任务卡仍直接显示原始 cron")
			c.require(d.uiRoot.Navigator().Current() == "automation",
				"navigator.Current()=%q，期望 automation", d.uiRoot.Navigator().Current())
			c.shot("nav-automation")
			// Edit: the inline form opens prefilled and cancelling restores the
			// card without changing the task.
			if !c.click("编辑") {
				return
			}
			d.drain(2)
			c.require(c.has("编辑自动化任务"), "点击「编辑」没有打开编辑表单")
			c.require(c.has("保存") && c.has("取消"), "编辑表单缺少保存/取消")
			c.shot("automation-edit")
			if !c.click("取消") {
				return
			}
			d.drain(2)
			c.require(c.has("每日构建检查"), "取消编辑后任务卡未恢复")
			c.shot("automation-edit-cancelled")
		})

	d.step("nav-plugins", "uiroot", "导航：插件",
		"侧栏点击「插件」→ plugins screen 渲染，显示工具目录", func(c *check) {
			if !c.click("插件") {
				return
			}
			c.mustText("插件", "工具目录")
			c.require(d.uiRoot.Navigator().Current() == "plugins",
				"navigator.Current()=%q，期望 plugins", d.uiRoot.Navigator().Current())
			c.shot("nav-plugins")
		})

	d.step("nav-session", "uiroot", "导航：会话",
		"侧栏点击会话行→ session screen 渲染，composer 出现", func(c *check) {
			label := d.currentSessionTitle()
			if _, ok := d.find(label); !ok {
				label = "驱动会话"
			}
			if !c.click(label) {
				return
			}
			d.drain(3)
			d.syncSession()
			c.mustText("输入消息…")
			c.require(d.uiRoot.Navigator().Current() == "session",
				"navigator.Current()=%q，期望 session", d.uiRoot.Navigator().Current())
			c.shot("nav-session")
		})
}

func (d *drive) runThemeLanguageSteps() {
	d.step("theme-dark", "settings", "主题：设置页切暗色",
		"设置→外观→点击「深色」→ 主题变暗且截图；再点「浅色」恢复", func(c *check) {
			d.resetToSession()
			if !c.click("设置") {
				return
			}
			if !c.has("外观") {
				c.failf("设置导航没有「外观」项")
				return
			}
			if !c.click("外观") {
				return
			}
			if !c.click("深色") {
				return
			}
			d.drain(2)
			c.require(d.uiRoot.Theme().Dark(), "点击「深色」后 theme.Dark()=false")
			c.shot("theme-dark")
			if !c.click("浅色") {
				return
			}
			d.drain(2)
			c.require(!d.uiRoot.Theme().Dark(), "点击「浅色」后 theme.Dark()=true")
			c.shot("theme-light")
		})

	d.step("lang-en", "settings", "语言：设置页切英文",
		"设置→外观→语言 Select 选 English → 界面切英文；再切回中文", func(c *check) {
			// Settings is already open on the Appearance page from theme-dark.
			if !d.uiRoot.Navigator().SettingsOpen() {
				if !c.click("设置") {
					return
				}
				if !c.click("外观") {
					return
				}
			}
			if !c.click("中文") {
				return
			}
			d.drain(2)
			if !c.click("English") {
				return
			}
			d.drain(2)
			c.require(d.uiRoot.Locale().Language() == "en",
				"选择 English 后 locale.Language()=%q", d.uiRoot.Locale().Language())
			c.mustText("Settings", "Appearance", "Interface language")
			c.shot("lang-en")
			// Switch back to Chinese through the same native select.
			if !c.click("English") {
				return
			}
			d.drain(2)
			if !c.click("中文") {
				return
			}
			d.drain(2)
			c.require(d.uiRoot.Locale().Language() == "zh",
				"选择中文后 locale.Language()=%q", d.uiRoot.Locale().Language())
			if !c.has("设置") {
				c.failf("切回中文后设置界面未恢复中文")
			}
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
			c.shot("lang-zh")
		})
}

func (d *drive) runSidebarSteps() {
	d.step("sidebar-collapse", "uiroot", "侧栏：Cmd+B 折叠/展开",
		"Cmd+B 折叠为 64DIP rail，再 Cmd+B 展开；折叠时 rail 图标可用", func(c *check) {
			d.resetToSession()
			d.key(ui.Cmd, ui.KeyB)
			d.drain(2)
			c.require(d.uiRoot.SidebarCollapsed(), "Cmd+B 后 SidebarCollapsed()=false")
			c.shot("sidebar-collapsed")
			// Every rail destination must still be a labeled control. The old
			// 首页 destination is gone: the new-chat landing replaces it.
			missing := []string{}
			for _, label := range []string{"新对话", "自动化", "插件", "设置"} {
				if _, ok := d.find(label); !ok {
					missing = append(missing, label)
				}
			}
			c.require(len(missing) == 0, "折叠 rail 缺少可点击项：%v", missing)
			if len(missing) == 0 {
				rect, _ := d.find("自动化")
				d.moveTo(rect)
				d.frame()
				c.shot("sidebar-rail-hover")
				if !c.click("自动化") {
					return
				}
				c.require(d.uiRoot.Navigator().Current() == "automation",
					"折叠 rail 点击自动化后 Current()=%q", d.uiRoot.Navigator().Current())
				d.uiRoot.Navigator().Push("session")
				d.frame()
			}
			d.key(ui.Cmd, ui.KeyB)
			d.drain(2)
			c.require(!d.uiRoot.SidebarCollapsed(), "再次 Cmd+B 后 SidebarCollapsed()=true")
			c.shot("sidebar-expanded")
		})

	d.step("sidebar-workspace-new-chat", "uiroot", "侧栏：按工作区分组 + 工作区行新建会话",
		"侧栏没有「视图」菜单；新添加的空工作区按最近活动排在最上并自动成为当前；其行右侧「在 “X” 中新建会话」点击后在该工作区创建会话并进入会话页", func(c *check) {
			d.resetToSession()
			for _, label := range []string{"视图", "按名称排序", "平铺"} {
				c.require(!d.has(label), "侧栏仍有已移除的视图菜单「%s」", label)
			}
			workspaces, err := d.root.Require(contracts.KeyWorkspace)
			if err != nil {
				c.failf("require workspace: %v", err)
				return
			}
			// A freshly added, empty workspace (the direct-picker flow): it is
			// current at once and therefore visible with its own ⊕.
			// A short, deterministic folder name: the backend derives the
			// workspace name from it, and a very long random name overflows the
			// 280 DIP group row and pushes its row actions out of the sidebar (a
			// separate layout finding reported to the Lead, not asserted here).
			extraRoot := filepath.Join(d.dataDir, "zk-new")
			if err := os.MkdirAll(extraRoot, 0o700); err != nil {
				c.failf("mkdir extra workspace: %v", err)
				return
			}
			created, err := workspaces.Create(context.Background(), "", extraRoot)
			if err != nil {
				c.failf("create extra workspace: %v", err)
				return
			}
			d.frame()
			label := "在“" + created.Name + "”中新建会话"
			rect, ok := d.find(label)
			if !ok {
				c.failf("新工作区行缺少 ⊕「%s」；texts=%v", label, d.texts())
				return
			}
			// Groups are ordered by recent activity: the fresh workspace (no
			// sessions, CreatedAt newest) sits above the seeded one.
			if oldRect, ok := d.find("drive-workspace"); ok && rect.Y >= oldRect.Y {
				c.failf("新工作区没有按最近活动排到顶部：new y=%.0f, old y=%.0f", rect.Y, oldRect.Y)
			}
			before := len(d.chat().Sessions())
			// tester.Click validates that the clicked point really reaches the
			// labeled control (a raw ClickAt would hit the row and only fold it).
			if err := d.click(label); err != nil {
				c.failf("点击工作区行 ⊕ 失败：%v", err)
				return
			}
			d.drain(3)
			after := d.chat().Sessions()
			if len(after) != before+1 {
				c.failf("点击工作区行 ⊕ 后会话数 %d → %d，期望 +1", before, len(after))
				return
			}
			active := d.chat().ActiveSession()
			bound := false
			for _, info := range after {
				if info.ID == active && strings.TrimSpace(info.WorkspaceID) == created.ID {
					bound = true
				}
			}
			c.require(bound, "新会话没有绑定到点击的工作区（active=%q workspace=%q）", active, created.ID)
			c.require(d.uiRoot.Navigator().Current() == "session",
				"点击 ⊕ 后没有进入会话页（Current=%q）", d.uiRoot.Navigator().Current())
			c.shot("sidebar-workspace-new-chat")
		})

	d.step("sidebar-footer", "uiroot", "侧栏：底部设置入口",
		"底部「设置」入口 dock 在侧栏底部、可点并可打开设置；侧栏不再有归档视图开关", func(c *check) {
			d.resetToSession()
			// Archived sessions moved to the settings 归档 page: the old view
			// toggle must not exist anywhere in the sidebar.
			for _, label := range []string{"显示已归档", "隐藏已归档"} {
				c.require(!d.has(label), "侧栏仍有归档视图开关「%s」", label)
			}
			settingsRect, ok := d.find("设置")
			if !ok {
				c.failf("侧栏 footer 没有设置入口")
				return
			}
			if settingsRect.Y < float32(d.h)*0.5 {
				c.warnf("设置入口没有 dock 在侧栏底部（y=%.0f）", settingsRect.Y)
			}
			if !c.click("设置") {
				return
			}
			c.require(d.uiRoot.Navigator().SettingsOpen(), "点击底部「设置」后设置未打开")
			c.shot("sidebar-settings-entry")
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
		})
}

func (d *drive) runWindowSizeSteps() {
	d.step("window-1024", "design", "窄窗 1024×720：发送按钮与标题可见",
		"会话页「发送」按钮存在且 rect 在窗口内、标题文本可见", func(c *check) {
			d.setSize(1024, 720)
			d.resetToSession()
			c.shot("window-1024")
			c.checkSendInWindow()
			c.checkTitleVisible(d.currentSessionTitle())
			// The send control must actually send at this width.
			if _, err := d.sendMessage("窄窗发送测试", 20*time.Second); err != nil {
				c.failf("窄窗发送失败：%v", err)
			}
			d.drain(2)
			c.shot("window-1024-sent")
		})

	d.step("window-900", "design", "最小窗 900×600：发送按钮与标题可见",
		"会话页「发送」按钮存在且 rect 在窗口内、标题文本可见", func(c *check) {
			d.setSize(900, 600)
			d.resetToSession()
			c.shot("window-900")
			c.checkSendInWindow()
			c.checkTitleVisible(d.currentSessionTitle())
			d.setSize(1280, 800)
			d.frame()
		})
}

func (d *drive) runToastSteps() {
	d.step("toast-action-undo", "uiroot", "通知：带「撤销」动作的原生 toast",
		"NotifyAction（ActionNotifier）→ 原生 toast 显示提示与「撤销」按钮；点撤销执行回调并关闭", func(c *check) {
			d.resetToSession()
			notifier := d.uiRoot.Notifier()
			undone := false
			notification := notifier.NotifyAction(contracts.NotifySuccess,
				"测试撤销：已删除记忆", "撤销", func() { undone = true })
			defer notifier.Dismiss(notification.ID)
			if !d.waitText("测试撤销：已删除记忆", 3*time.Second) {
				c.failf("NotifyAction 后提示文本未出现")
				return
			}
			c.require(c.has("撤销"), "带动作的 toast 没有「撤销」按钮")
			c.shot("toast-action-undo")
			if !c.click("撤销") {
				return
			}
			d.tester.Advance(400 * time.Millisecond)
			d.frame()
			c.require(undone, "点击「撤销」后回调没有执行")
			c.require(!c.has("撤销"), "点击撤销后 toast 未关闭")
			d.tester.Advance(6 * time.Second)
			d.frame()
		})

	d.step("toast-native", "uiroot", "通知：原生 toast 出现/过期",
		"notifier.Notify → 提示文本出现并截图；Advance(5s) 后按 TTL 过期消失", func(c *check) {
			d.resetToSession()
			notifier := d.uiRoot.Notifier()
			notification := notifier.Notify(contracts.NotifySuccess, "驱动通知：操作完成")
			defer notifier.Dismiss(notification.ID)
			if !d.waitText("驱动通知：操作完成", 3*time.Second) {
				c.failf("Notify 后提示文本未出现")
				return
			}
			// Let the native toast finish its fade-in before the screenshot.
			d.tester.Advance(400 * time.Millisecond)
			d.frame()
			c.shot("toast-success")
			d.tester.Advance(5 * time.Second)
			d.frame()
			c.require(!d.has("驱动通知：操作完成"), "toast 超过 TTL 后仍显示")
			_ = notification
		})
}
