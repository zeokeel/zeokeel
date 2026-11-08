package main

// H-series Home steps: recent conversations are resumable from the front page,
// and the composer's context line (workspace/model/mode) is interactive.

import (
	"github.com/egoist/mygo/ui"
)

func (d *drive) runHomeSteps() {
	d.step("home-recent-sessions", "shell", "Home：最近会话可点击进入",
		"Home 显示「最近会话」列表；点击一条会话行 → 打开该会话并进入会话页", func(c *check) {
			d.resetToSession()
			if err := d.uiRoot.Navigator().Push("home"); err != nil {
				c.failf("Push(home): %v", err)
				return
			}
			d.drain(2)
			if !c.has("最近会话") {
				c.failf("Home 没有「最近会话」区域")
				return
			}
			// Pick a visible recent conversation row.
			title := ""
			for _, info := range d.chat().Sessions() {
				if info.Archived {
					continue
				}
				if trimmed := info.Title; trimmed != "" && d.has(trimmed) {
					title = trimmed
					break
				}
			}
			if title == "" {
				c.failf("Home 最近会话列表里找不到可点击的会话标题; texts=%v", paletteTailTexts(d))
				return
			}
			c.shot("home-recent")
			if !c.click(title) {
				return
			}
			d.drain(3)
			c.require(d.uiRoot.Navigator().Current() == "session",
				"点击最近会话后导航状态=%q，期望 session", d.uiRoot.Navigator().Current())
			c.require(d.has("输入消息…"), "点击最近会话后 composer 未出现")
			d.syncSession()
			c.shot("home-recent-opened")
		})

	d.step("home-context-controls", "shell", "Home：上下文行（工作区/模型/模式）可交互",
		"Home composer 上下文行显示模型与模式；点模型/模式 chip 打开菜单并可切换；若菜单含设置入口则深链到设置", func(c *check) {
			d.resetToSession()
			if err := d.uiRoot.Navigator().Push("home"); err != nil {
				c.failf("Push(home): %v", err)
				return
			}
			d.drain(2)
			// Context line: model + mode controls are interactive menus.
			modelLabel := ""
			for _, label := range []string{"drive-model"} {
				if d.has(label) {
					modelLabel = label
					break
				}
			}
			if modelLabel == "" {
				c.failf("Home 上下文行没有模型 chip; texts=%v", paletteTailTexts(d))
				return
			}
			if !c.click(modelLabel) {
				return
			}
			d.drain(2)
			if !c.has("打开模型设置") && !c.has("DeepSeek") && !c.has("工作区模型") {
				c.warnf("Home 模型菜单既没有模型列表也没有设置入口")
				c.shot("home-context-model-menu")
			}
			openedSettings := false
			for _, entry := range []string{"打开模型设置…", "打开模型设置", "打开设置"} {
				if d.has(entry) {
					if c.click(entry) {
						d.drain(2)
						openedSettings = d.uiRoot.Navigator().SettingsOpen()
					}
					break
				}
			}
			if openedSettings {
				c.note("Home 模型菜单可以深链到设置面")
				if c.has("Base URL") || c.has("服务商") || c.has("模型") {
					c.note("深链落在模型/服务商设置页")
				}
				d.uiRoot.Navigator().CloseSettings()
				d.frame()
			} else {
				d.key(0, ui.KeyEscape)
				d.drain(2)
			}
			// The mode chip menu is a real menu too.
			if c.has("普通") && c.click("普通") {
				d.drain(2)
				modeMenu := c.has("计划") || c.has("创作")
				if !modeMenu {
					c.warnf("Home 模式 chip 没有打开模式菜单")
				} else {
					c.shot("home-context-mode-menu")
					// Restore standard mode.
					if c.has("普通") {
						_ = c.click("普通")
					}
				}
				d.key(0, ui.KeyEscape)
				d.drain(2)
			} else {
				c.warnf("Home 上下文行没有模式 chip")
			}
			c.shot("home-context-controls")
		})
}
