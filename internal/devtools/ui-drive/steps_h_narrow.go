package main

// H-series narrow-layout steps: the session Memory tab must not repeat its
// title, the conversation header keeps the title readable and the goal chip
// compact when the content column is narrow (1024 + inspector drawer), and the
// very narrow composer keeps only the attach menu, the single session menu and
// send (the model chip folds into that menu) instead of an unlabeled » overflow.

import (
	"context"
	"github.com/egoist/mygo/ui"
)

func (d *drive) runHNarrowSteps() {
	d.step("session-memory-tab", "session", "记忆页签：无正文重复大标题（H25）",
		"会话「记忆」页签下首屏不再出现第二个「记忆」大标题，直接是搜索记忆/记忆条目", func(c *check) {
			d.resetToSession()
			if !c.click("记忆") {
				return
			}
			d.drain(3)
			// The tab strip already renders the label twice (native tab label +
			// its text child); a body title would add one more.
			if !c.has("搜索记忆") {
				c.failf("记忆页签没有显示「搜索记忆」")
				return
			}
			count := d.countExact("记忆")
			c.require(count <= 2, "记忆页仍显示正文重复「记忆」大标题（exact 出现 %d 次，期望 ≤2）", count)
			c.shot("session-memory-tab")
		})

	d.step("session-narrow-header", "session", "窄列 header：标题优先 + goal chip 紧凑（N1）",
		"1024 窗口 + 右栏 drawer 使主列 <560 DIP：会话标题保持可读，goal chip 只显示状态 label、不显示目标正文", func(c *check) {
			d.resetToSession()
			goalText := "窄列标题优先与目标紧凑显示的测试目标"
			tracker := d.uiRoot.GoalTracker()
			if _, err := tracker.Set(context.Background(), goalText); err != nil {
				c.failf("GoalTracker.Set 失败：%v", err)
				return
			}
			defer func() {
				tracker.Clear()
				d.uiRoot.SetRightPanelOpen(false)
				d.setSize(1280, 800)
				d.frame()
			}()
			d.setSize(1024, 720)
			d.uiRoot.SetRightPanelOpen(true)
			d.drain(3)
			c.require(d.uiRoot.RightPanelOpen(), "1024 窗口下右侧检查器未以 drawer 打开")
			if !c.has("目标") {
				c.failf("窄列 header 没有 goal chip 状态 label「目标」")
				return
			}
			// The chip's accessibility label always carries the full goal text;
			// its visible Text child must be absent in the compact header.
			c.require(d.countContaining(goalText) == 1,
				"窄列 header 仍显示目标正文（应只有无障碍 label 含全文，实际 %d 处）", d.countContaining(goalText))
			title := d.currentSessionTitle()
			c.require(d.has(title), "窄列 header 的会话标题不可见（应保留 ≥120 DIP 可读宽度）")
			c.shot("session-narrow-header")
			// With the drawer closed the column is wide again: the chip shows
			// the goal text (truncated visually, full label in the frame).
			d.uiRoot.SetRightPanelOpen(false)
			d.drain(3)
			if d.countContaining(goalText) < 2 {
				c.warnf("宽列 header 没有显示目标正文（宽列应显示截断正文 + 无障碍 label）")
			}
		})

	d.step("composer-very-narrow", "session", "极窄 composer（<480 DIP）",
		"704 窗口（composer 卡 <480）：附件菜单 + 会话设置菜单 + 发送；模型 chip 折进会话设置菜单；无无标签 »、无独立 / chip", func(c *check) {
			d.setSize(704, 720)
			defer func() {
				d.setSize(1280, 800)
				d.frame()
			}()
			d.resetToSession()
			if c.has("搜索记忆") {
				if !c.click("对话") {
					c.failf("无法切回「对话」页签")
					return
				}
				d.drain(2)
			}
			d.drain(3)
			for _, want := range []string{"附件", "发送"} {
				c.require(c.has(want), "极窄 composer 缺少「%s」", want)
			}
			c.require(!d.hasExactText("»"), "极窄 composer 出现了无标签 » 溢出按钮")
			if d.has("drive-model") || d.has("未配置") {
				c.failf("极窄 composer 仍直接显示模型 chip（应折进会话设置菜单）")
			}
			c.shot("composer-very-narrow")
			// The session menu carries the folded model entry and the low-frequency groups.
			if !c.click("工作区权限") && !c.click("会话设置") {
				return
			}
			d.drain(2)
			c.mustText("模型：", "打开模型设置…", "权限模式", "对话模式", "思考强度", "插入预设")
			if !c.has("打开行为设置") {
				c.failf("「会话设置」菜单缺少「打开行为设置…」")
			}
			c.shot("composer-very-narrow-menu")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			// The attach menu keeps both file picking and the manual path entry.
			if c.click("附件") {
				d.drain(2)
				c.mustText("选择文件…", "手动输入路径…")
				d.key(0, ui.KeyEscape)
				d.drain(2)
			}
			// Typing "/" still opens the command menu even though no chip exists.
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.clearDraft()
			d.typeText("/新")
			d.drain(2)
			if !d.has("新会话") {
				c.shot("composer-very-narrow-slash-fail")
				c.failf("极窄 composer 输入 / 没有打开命令菜单；texts=%v", paletteTailTexts(d))
				return
			}
			c.shot("composer-very-narrow-slash")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
		})
}
