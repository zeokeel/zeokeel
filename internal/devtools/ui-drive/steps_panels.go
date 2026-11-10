package main

// Right-sidebar steps: the DSH-aligned sidebar replaced the retired 8-pane
// inspector. It opens on the 开始 guide (工作区文件 / 新建终端 cards), has one
// tab strip (开始 | 文件 | 终端 | opened files), opens a workspace file as a
// preview tab with its folded unified diff, and runs commands in the 终端 page.
// The old pane selector, 概览/工具/日志/变更/团队/任务 panes and their commands
// are gone: team/tasks coverage lives in steps_team.go and the log is folded
// into the file tabs.

import (
	"time"
)

// sidebarPageTabs are the right sidebar's fixed pages in strip order.
var sidebarPageTabs = []string{"开始", "文件", "终端"}

func (d *drive) runPanelSteps() {
	d.step("panel-open", "shell", "右栏：打开右侧栏 → 开始 guide",
		"点击顶栏「打开右侧栏」→ 侧栏出现开始 guide：两个等宽卡片 工作区文件/新建终端 + 标签页 开始|文件|终端；旧的 8-pane 选择器与概览/工具/日志/变更/团队/任务 pane 已移除", func(c *check) {
			d.resetToSession()
			d.uiRoot.SetRightPanelOpen(false)
			d.frame()
			if !c.click("打开右侧栏") {
				return
			}
			c.require(d.uiRoot.RightPanelOpen(), "点击「打开右侧栏」后侧栏未打开")
			c.mustText("开始", "工作区文件", "新建终端")
			missing := []string{}
			for _, tab := range sidebarPageTabs {
				if _, ok := d.find(tab); !ok {
					missing = append(missing, tab)
				}
			}
			c.require(len(missing) == 0, "右侧栏标签页缺少：%v", missing)
			for _, retired := range []string{"切换面板", "概览", "工具", "日志", "变更"} {
				c.require(!d.hasExactText(retired), "右侧栏仍显示已移除的旧 pane「%s」", retired)
			}
			c.shot("panel-guide")
		})

	d.step("panel-guide-cards", "shell", "右栏：guide 卡片进入 文件/终端 页",
		"开始 guide 点「工作区文件」→ 文件页（工作区树 + 产物区）；点「开始」标签回 guide；点「新建终端」→ 终端页（命令输入 + 运行）", func(c *check) {
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			if !c.click("开始") {
				return
			}
			d.drain(2)
			if !c.click("工作区文件") {
				return
			}
			if !d.waitText("README.md", 5*time.Second) {
				c.failf("文件页没有显示工作区树（README.md）；texts=%v", paletteTailTexts(d))
				return
			}
			c.mustText("交付区")
			c.shot("panel-files-page")
			if !c.click("开始") {
				return
			}
			d.drain(2)
			if !c.click("新建终端") {
				return
			}
			d.drain(2)
			c.mustText("运行")
			if _, ok := d.find("输入命令…"); !ok && !c.has("命令") {
				c.failf("终端页没有命令输入（「命令」/「输入命令…」缺失）")
				return
			}
			c.shot("panel-terminal-page")
			if !c.click("开始") {
				return
			}
			d.drain(2)
		})

	d.step("panel-files-preview", "shell", "右栏：文件页打开 README.md → 文件标签预览",
		"文件页点 README.md → 打开其文件标签页（文件名 + 目录 + 复制路径/重新读取 + Markdown 预览）；预览显示 README 正文", func(c *check) {
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			if !c.click("文件") {
				return
			}
			if !d.waitText("README.md", 5*time.Second) {
				c.failf("文件页没有显示 README.md；texts=%v", paletteTailTexts(d))
				return
			}
			if err := d.click("README.md"); err != nil {
				c.failf("点击 README.md 失败：%v", err)
				return
			}
			if !d.waitText("驱动工作区", 5*time.Second) {
				c.failf("README.md 预览没有显示文件正文；texts=%v", paletteTailTexts(d))
				return
			}
			c.mustText("README.md", "复制路径")
			c.shot("panel-file-preview")
		})

	d.step("panel-file-tabs", "shell", "右栏：多个文件标签可切换",
		"文件页再打开 notes.txt → 两个文件标签并存；点 README.md 标签切回其预览（正文可见），再点 notes.txt 标签切回（文件标签头可见）", func(c *check) {
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			if !c.click("文件") {
				return
			}
			if !d.waitText("notes.txt", 5*time.Second) {
				c.failf("文件页没有显示 notes.txt；texts=%v", paletteTailTexts(d))
				return
			}
			if err := d.click("notes.txt"); err != nil {
				c.failf("点击 notes.txt 失败：%v", err)
				return
			}
			d.drain(2)
			c.require(d.hasExactText("README.md") && d.hasExactText("notes.txt"),
				"打开两个文件后标签条没有两个文件标签；texts=%v", paletteTailTexts(d))
			c.shot("panel-file-tabs")
			// Switch back to the README tab: its Markdown preview renders the
			// file body, which proves the page followed the tab choice.
			if err := d.click("README.md"); err != nil {
				c.failf("切回 README 标签失败：%v", err)
				return
			}
			d.drain(2)
			if !d.waitText("驱动工作区", 5*time.Second) {
				c.failf("切回 README 标签后没有显示其预览正文；texts=%v", paletteTailTexts(d))
				return
			}
			c.shot("panel-file-tab-switched")
		})

	d.step("panel-file-diff", "shell", "右栏：文件标签内折叠的改动 diff",
		"文件页点已修改的 README.md → 文件标签出现「查看改动」折叠段（+N -M）；展开后显示统一 diff", func(c *check) {
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			if !c.click("文件") {
				return
			}
			if !d.waitText("README.md", 5*time.Second) {
				c.failf("文件页没有显示 README.md；texts=%v", paletteTailTexts(d))
				return
			}
			if err := d.click("README.md"); err != nil {
				c.failf("点击 README.md 失败：%v", err)
				return
			}
			if !d.waitText("查看改动", 5*time.Second) {
				c.failf("已修改的 README 文件标签没有「查看改动」折叠段；texts=%v", paletteTailTexts(d))
				return
			}
			if !c.has("+") || !c.has("-") {
				c.note("改动统计行未渲染，直接展开折叠段")
			}
			c.shot("panel-file-diff-folded")
			if err := d.click("查看改动"); err != nil {
				c.failf("展开「查看改动」失败：%v", err)
				return
			}
			d.tester.Advance(200 * time.Millisecond)
			d.drain(2)
			c.shot("panel-file-diff")
		})

	d.step("panel-terminal-run", "shell", "右栏：终端页运行 echo 命令",
		"终端标签页输入 echo ui-drive-terminal → 点「运行」→ 输出包含该文本", func(c *check) {
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			if !c.click("终端") {
				return
			}
			d.drain(2)
			// Focus the terminal input by its accessible name; the composer's
			// slash button shares the visible 「命令」word, but the input's
			// label is the exact one here (the composer is not the active page).
			if err := d.click("命令"); err != nil {
				c.failf("终端页没有可聚焦的命令输入：%v", err)
				return
			}
			d.frame()
			d.typeText("echo ui-drive-terminal")
			d.frame()
			if !c.click("运行") {
				return
			}
			if !d.waitText("ui-drive-terminal", 8*time.Second) {
				c.failf("终端命令输出未出现；texts=%v", paletteTailTexts(d))
				return
			}
			c.shot("panel-terminal-run")
		})

	d.step("panel-close", "shell", "右栏：关闭侧栏",
		"点击「收起右侧栏」→ 侧栏关闭", func(c *check) {
			label := "收起右侧栏"
			if !d.has(label) {
				label = "打开右侧栏"
			}
			// The toggle button's label reflects the current state; the open
			// label means it is already closed.
			if label == "收起右侧栏" {
				if !c.click(label) {
					return
				}
				d.drain(2)
			}
			c.require(!d.uiRoot.RightPanelOpen(), "点击后右侧栏仍打开")
			c.shot("panel-closed")
		})
}
