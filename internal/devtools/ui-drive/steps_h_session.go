package main

// H-series session steps: floating suggestion menus must not push the
// transcript (overlay instead of in-flow insertion), the queued-prompts popover
// can be cancelled, and the message actions expose a persistent copy entry.

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// stableTranscriptRect returns the rect of the "消息操作" control (the ⋯ entry
// of a finished assistant turn's compact meta line), which sits at the end of the
// transcript and moves whenever the transcript layout is displaced by an
// inserted in-flow panel.
func (d *drive) stableTranscriptRect() (ui.Rect, bool) {
	if rect, ok := d.find("消息操作"); ok {
		return rect, true
	}
	// A conversation without finished assistant turns has no meta line; the
	// composer card's top edge is the next stable anchor.
	if rect, ok := d.find("会话输入框"); ok {
		return rect, true
	}
	return ui.Rect{}, false
}

// rectDelta reports the largest axis movement between two rects.
func rectDelta(before, after ui.Rect) float32 {
	dx := after.X - before.X
	dy := after.Y - before.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}

func (d *drive) runHSessionSteps() {
	d.step("overlay-slash-no-layout-shift", "session", "斜杠菜单：浮层不推布局",
		"打开斜杠菜单前后，transcript 尾部控件 rect 不变（菜单是浮层而非插入布局流）", func(c *check) {
			d.resetToSession()
			// Ensure at least one finished turn exists so the transcript has a
			// stable tail control.
			if d.lastAssistantText() == "" {
				d.server.SetMode(scriptAnswer)
				if _, err := d.sendMessage("布局稳定性测试", 20*time.Second); err != nil {
					c.failf("准备会话内容失败：%v", err)
					return
				}
			}
			d.resetToSession()
			before, ok := d.stableTranscriptRect()
			if !ok {
				c.failf("找不到 transcript 稳定控件（消息操作/会话输入框）")
				return
			}
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.clearDraft()
			// A leading space makes this a fresh draft: an earlier Esc dismissal
			// is remembered per exact draft, so retyping "/" alone stays closed.
			d.typeText(" /")
			d.drain(2)
			c.require(d.has("打开设置") || d.has("新会话"), "斜杠菜单未出现")
			after, ok := d.stableTranscriptRect()
			if !ok {
				c.failf("打开斜杠菜单后 transcript 稳定控件消失（被菜单覆盖/挤出）")
				return
			}
			c.note("斜杠菜单前后 rect 位移 %.1f DIP（before=%+v after=%+v）", rectDelta(before, after), before, after)
			c.require(rectDelta(before, after) <= 1, "斜杠菜单把 transcript 挤动了 %.1f DIP（应为浮层）", rectDelta(before, after))
			c.shot("overlay-slash-no-layout-shift")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
		})

	d.step("overlay-mention-no-layout-shift", "session", "＠菜单：浮层不推布局",
		"打开 @ 菜单前后 transcript 尾部控件 rect 不变（菜单是浮层）", func(c *check) {
			d.resetToSession()
			before, ok := d.stableTranscriptRect()
			if !ok {
				c.failf("找不到 transcript 稳定控件")
				return
			}
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.clearDraft()
			d.typeText("@REA")
			d.drain(3)
			if !c.has("/README.md") && !c.has("工作区文件") && !c.has("会话") {
				c.failf("@ 菜单未出现")
				d.clearDraft()
				return
			}
			after, ok := d.stableTranscriptRect()
			if !ok {
				c.failf("打开 @ 菜单后 transcript 稳定控件消失")
				d.clearDraft()
				return
			}
			c.note("@ 菜单前后 rect 位移 %.1f DIP（before=%+v after=%+v）", rectDelta(before, after), before, after)
			c.require(rectDelta(before, after) <= 1, "@ 菜单把 transcript 挤动了 %.1f DIP（应为浮层）", rectDelta(before, after))
			c.shot("overlay-mention-no-layout-shift")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
		})

	d.step("message-actions-copy", "session", "消息动作：元信息行 + ⋯ 菜单",
		"每条助手消息的紧凑元信息（时间 + 用时/紧凑 tokens + ⋯）在消息顶部/与过程摘要同一行；⋯ 菜单提供复制消息/重新生成/用量明细", func(c *check) {
			d.server.SetMode(scriptAnswer)
			// Use a fresh one-turn conversation: the long driving session is
			// scrolled, so its message rows are not reliable click targets.
			chat := d.chat()
			original := chat.ActiveSession()
			sessionID, err := chat.NewSession("复制测试会话")
			if err != nil {
				c.failf("新建复制测试会话失败：%v", err)
				return
			}
			defer func() {
				_ = chat.DeleteSession(sessionID)
				if original != "" {
					_ = chat.SetActiveSession(original)
				}
				d.syncSession()
				d.frame()
			}()
			d.sessionID = sessionID
			if err := d.uiRoot.Navigator().Push("session"); err != nil {
				c.failf("Push(session): %v", err)
				return
			}
			d.frame()
			reply, err := d.sendMessage("复制入口测试", 20*time.Second)
			if err != nil {
				c.failf("准备助手消息失败：%v", err)
				return
			}
			if strings.TrimSpace(reply) == "" {
				c.failf("发送后没有助手回复")
				return
			}
			d.drain(2)
			// Pointer away from the transcript: the meta line is part of the
			// frame without hover or a pin (time and compact usage are resident).
			d.tester.Move(4, 4)
			d.frame()
			c.require(c.has("消息操作"), "助手消息底部没有常驻「消息操作」⋯ 入口")
			c.require(!c.has("用时"), "消息元信息行仍常驻「用时」遥测（减法后应只在 ⋯ 用量明细里）")
			c.shot("message-actions-copy")
			if !c.click("消息操作") {
				return
			}
			d.drain(2)
			c.require(c.has("复制消息"), "「消息操作」菜单缺少「复制消息」")
			c.require(c.has("重新生成"), "「消息操作」菜单缺少「重新生成」")
			c.require(c.has("复制消息"), "「消息操作」菜单缺少「复制消息」")
			c.require(!c.has("有帮助") && !c.has("没帮助"),
				"「消息操作」菜单不应再有评分项")
			if err := d.tester.ChooseMenuItem("复制消息"); err != nil {
				c.failf("choose 复制消息: %v", err)
				return
			}
			d.drain(2)
			got := strings.TrimSpace(d.tester.Clipboard())
			c.require(got == strings.TrimSpace(reply),
				"点击复制后剪贴板内容不匹配（got %q want %q）", shortText(got), shortText(reply))
			c.shot("message-actions-copy-done")
		})

	d.step("queue-popover-cancel", "session", "队列：排队弹层可取消",
		"运行中点「排队」→ 队列弹层列出待发提示 → 点「取消」→ 队列清空", func(c *check) {
			d.server.SetMode(scriptSlowAnswer)
			defer d.server.SetMode(scriptAnswer)
			previous := d.settings().Get(contracts.SettingSendWhileRunning, "")
			if err := d.settings().Set(contracts.SettingSendWhileRunning, "true"); err != nil {
				c.failf("启用排队设置失败：%v", err)
				return
			}
			defer func() {
				_ = d.settings().Set(contracts.SettingSendWhileRunning, previous)
			}()
			d.resetToSession()
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.clearDraft()
			d.typeText("慢速流式测试")
			d.frame()
			before, err := d.clickSend()
			if err != nil {
				c.failf("%v", err)
				return
			}
			if !d.waitRunning(false, 10*time.Second) {
				c.failf("发送后未进入运行中，无法测试排队")
				_, _ = d.waitTurn(before, 10*time.Second)
				return
			}
			// Queue one prompt while the turn runs.
			if err := d.focusComposer(); err != nil {
				// The composer input stays visible while running when queueing
				// is enabled; a missing input is a finding by itself.
				c.failf("运行中 composer 输入不可用：%v", err)
				_ = d.click("停止")
				_, _ = d.waitTurn(before, 10*time.Second)
				return
			}
			d.typeText("排队取消测试")
			d.frame()
			if _, ok := c.clickAny("排队", "排队发送"); !ok {
				_ = d.click("停止")
				_, _ = d.waitTurn(before, 10*time.Second)
				return
			}
			d.drain(2)
			if !c.has("已排队") && !c.has("排队取消测试") {
				c.failf("点「排队」后没有出现「已排队」标记")
			}
			// The badge opens the queue popover, where the queued prompt can be
			// cancelled explicitly (H15).
			if c.has("已排队") {
				badge, rect, ok := d.findPrefix("已排队")
				if !ok {
					c.failf("找不到「已排队 N 条」按钮")
					return
				}
				_ = badge
				d.clickRect(rect)
				d.drain(2)
			}
			c.shot("queue-popover")
			if !c.has("排队消息") {
				c.failf("队列弹层没有出现（缺少「排队消息」标题）")
				return
			}
			if !c.has("取消排队") {
				c.failf("队列弹层缺少「取消排队」入口")
				return
			}
			if !c.click("取消排队") {
				return
			}
			d.drain(2)
			c.require(!c.has("已排队"), "取消后队列徽标仍显示")
			c.require(!c.has("排队取消测试"), "取消后排队提示仍显示")
			c.shot("queue-popover-cancelled")
			if _, ok := d.find("停止"); ok {
				_ = d.click("停止")
			}
			_, _ = d.waitTurn(before, 15*time.Second)
			d.drain(2)
		})
}
