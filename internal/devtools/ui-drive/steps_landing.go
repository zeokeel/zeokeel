package main

// Landing steps (replacing the retired Home navigation): the product now lands
// on the conversation screen's new-chat hero, and the sidebar's 新对话 action is
// the way to start a fresh conversation. These steps keep the landing's
// discoverability and the conversation switch covered after Home left the
// sidebar.

import (
	"github.com/egoist/mygo/ui"
)

func (d *drive) runLandingSteps() {
	d.step("landing-new-chat", "shell", "落地页：侧栏「新对话」→ hero + composer",
		"任意屏幕点侧栏「新对话」→ 会话页新对话落地（hero「开始新对话」+ composer）；导航状态 session，侧栏不再有「首页」项", func(c *check) {
			d.resetToSession()
			// Go somewhere else first: the reset below must land through the
			// sidebar action, not because the session was already on screen.
			if err := d.uiRoot.Navigator().Push("automation"); err != nil {
				c.failf("Push(automation): %v", err)
				return
			}
			d.drain(2)
			if !c.click("新对话") {
				return
			}
			d.drain(3)
			c.mustText("开始新对话", "输入消息…")
			c.require(d.uiRoot.Navigator().Current() == "session",
				"点「新对话」后导航状态=%q，期望 session", d.uiRoot.Navigator().Current())
			c.require(!d.hasExactText("首页"), "侧栏仍显示已移除的「首页」导航项")
			d.syncSession()
			c.shot("landing-new-chat")
		})

	d.step("landing-conversation-switch", "shell", "落地页：从侧栏会话列表切回已有会话",
		"hero 落地页 → 侧栏点已有会话标题 → 进入该会话，transcript 与 composer 出现", func(c *check) {
			if !d.has("开始新对话") {
				if !c.click("新对话") {
					return
				}
				d.drain(3)
			}
			// Pick a seeded conversation from the sidebar (skip the empty
			// landing session we are on).
			title := ""
			active := d.chat().ActiveSession()
			for _, info := range d.chat().Sessions() {
				if info.ID == active || info.Archived || info.Title == "" {
					continue
				}
				if d.hasExactText(info.Title) {
					title = info.Title
					break
				}
			}
			if title == "" {
				c.warnf("侧栏没有可点击的既有会话标题；跳过切换（当前会话=%q）", active)
				return
			}
			if !c.click(title) {
				return
			}
			d.drain(3)
			c.require(d.uiRoot.Navigator().Current() == "session",
				"切回会话后导航状态=%q", d.uiRoot.Navigator().Current())
			c.mustText("输入消息…")
			d.syncSession()
			c.shot("landing-conversation-switch")
		})

	// Keep the composer keyboard entry covered from the landing screen.
	d.step("landing-composer-focus", "shell", "落地页：输入框可聚焦并发送",
		"新对话落地页聚焦 composer 输入 → 输入文字草稿保留在输入框（不发送）", func(c *check) {
			if !d.has("开始新对话") {
				if !c.click("新对话") {
					return
				}
				d.drain(3)
			}
			if err := d.focusComposer(); err != nil {
				c.failf("聚焦 composer 失败：%v", err)
				return
			}
			d.typeText("落地页草稿")
			d.drain(2)
			c.shot("landing-composer")
			// Do not leave the draft behind: select all and delete.
			d.key(ui.Cmd, ui.KeyA)
			d.key(0, ui.KeyBackspace)
			d.drain(2)
		})
}
