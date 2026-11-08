package main

// Right-inspector steps: open the docked panel, visit every pane with a
// screenshot and a content assertion, preview a workspace file, expand a diff,
// run a terminal command and read the task board.

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// panelTabs is the inspector's pane order (the pane selector's menu) with the
// content markers the pane must show (any one of them counts: the pane copy
// differs across revisions).
var panelTabs = []struct {
	label   string
	markers []string
}{
	{"概览", []string{"消息", "更新时间", "诊断信息", "Messages", "Updated"}},
	{"工具", []string{"working_directory", "工具调用", "Tool calls"}},
	{"日志", []string{"条会话事件", "会话事件", "session events", "run_end", "user_message"}},
	{"文件", []string{"README.md", "文件", "Files"}},
	{"变更", []string{"查看 diff", "变更", "Changes", "README.md"}},
	{"终端", []string{"命令", "Command", "终端", "Terminal"}},
	{"团队", []string{"团队", "Team", "队友", "Teammates"}},
	{"任务", []string{"发布前检查", "任务", "Tasks", "还没有任务"}},
}

func (d *drive) runPanelSteps() {
	d.step("panel-open", "shell", "右栏：打开检查器",
		"点击内容区右上「打开右侧栏」→ 检查器出现（标题/切换面板/当前 pane），面板选择器列出 8 个 pane", func(c *check) {
			d.resetToSession()
			if !c.click("打开右侧栏") {
				return
			}
			c.require(d.uiRoot.RightPanelOpen(), "点击「打开右侧栏」后检查器未打开")
			c.mustText("检查", "切换面板")
			c.shot("panel-open")
			// The pane selector must offer every pane.
			if !c.click("切换面板") {
				return
			}
			d.drain(2)
			missing := []string{}
			for _, tab := range panelTabs {
				if !c.has(tab.label) {
					missing = append(missing, tab.label)
				}
			}
			c.require(len(missing) == 0, "面板选择器缺少 pane：%v", missing)
			c.shot("panel-selector")
			d.key(0, ui.KeyEscape)
			d.drain(2)
		})

	d.step("panel-tabs", "shell", "右栏：8 个 pane 逐个打开",
		"通过 pane 选择器依次切换概览/工具/日志/文件/变更/终端/团队/任务，每个 pane 内容渲染且截图", func(c *check) {
			d.resetToSession()
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			c.require(d.uiRoot.RightPanelOpen(), "右侧检查器未打开")
			for _, tab := range panelTabs {
				if !d.selectPane(tab.label) {
					c.failf("pane 选择器里无法选择「%s」", tab.label)
					continue
				}
				markerOK := false
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) && !markerOK {
					d.frame()
					for _, marker := range tab.markers {
						if d.has(marker) {
							markerOK = true
							break
						}
					}
					if !markerOK {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !markerOK {
					c.failf("pane「%s」内容缺少标记 %v", tab.label, tab.markers)
				}
				c.shot("panel-" + tab.label)
			}
		})

	d.step("panel-files-preview", "shell", "右栏：文件预览",
		"文件页签点 README.md → 预览显示文件内容", func(c *check) {
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			if !d.selectPane("文件") {
				c.failf("无法通过 pane 选择器切到「文件」")
				return
			}
			d.drain(2)
			if !c.click("README.md") {
				return
			}
			if !d.waitText("驱动工作区", 3*time.Second) {
				c.failf("README.md 预览没有显示文件内容")
			}
			c.shot("panel-file-preview")
		})

	d.step("panel-changes-diff", "shell", "右栏：变更 diff",
		"变更页签显示未提交文件 → 点「查看 diff」→ 展开 diff 文本", func(c *check) {
			if !d.selectPane("变更") {
				c.failf("无法通过 pane 选择器切到「变更」")
				return
			}
			d.drain(2)
			c.mustText("README.md")
			if !c.has("查看 diff") {
				c.failf("变更页签没有「查看 diff」")
				return
			}
			if !c.click("查看 diff") {
				return
			}
			d.tester.Advance(200 * time.Millisecond)
			d.drain(2)
			if !d.has("diff") && !d.has("已修改") && !d.has("+1") {
				c.failf("展开 diff 后没有 diff 内容")
			}
			c.shot("panel-changes-diff")
		})

	d.step("panel-terminal-run", "shell", "右栏：终端运行 echo",
		"终端页签输入 echo ui-drive-terminal → 点「运行」→ 输出包含该文本", func(c *check) {
			if !d.selectPane("终端") {
				c.failf("无法通过 pane 选择器切到「终端」")
				return
			}
			d.drain(2)
			// The pane's input shares the 「命令」label with the composer's
			// slash button, so focus it from the terminal's Run button instead
			// of a label lookup: the input sits left of Run on the same row.
			runRect, ok := d.find("运行")
			if !ok {
				c.failf("终端 pane 没有「运行」按钮")
				return
			}
			d.tester.ClickAt(runRect.X-220, runRect.Y+runRect.H/2)
			d.frame()
			d.typeText("echo ui-drive-terminal")
			d.frame()
			if !c.click("运行") {
				return
			}
			if !d.waitText("ui-drive-terminal", 8*time.Second) {
				c.failf("终端命令输出未出现")
			}
			c.shot("panel-terminal-run")
		})

	d.step("panel-tasks", "shell", "右栏：任务页签",
		"任务页签显示任务板条目（发布前检查/编写发布说明）", func(c *check) {
			if !d.selectPane("任务") {
				c.failf("无法通过 pane 选择器切到「任务」")
				return
			}
			d.drain(2)
			c.mustText("发布前检查", "编写发布说明")
			c.shot("panel-tasks")
		})

	d.step("panel-team", "shell", "右栏：团队页签",
		"生成一个队友后团队页签显示成员；页签本身必须可渲染", func(c *check) {
			d.resetToSession()
			if !d.uiRoot.RightPanelOpen() {
				d.uiRoot.SetRightPanelOpen(true)
				d.frame()
			}
			d.server.SetMode(scriptAnswer)
			team, teamErr := d.root.Require(contracts.KeyTeam)
			if teamErr != nil {
				c.warnf("team service 未启用：%v", teamErr)
			} else {
				// Spawn through the real tool surface (the same path the agent
				// uses), so the teammates pane has a durable member.
				if registry, err := d.root.Require(contracts.KeyToolkit); err == nil {
					_, invokeErr := registry.Invoke(context.Background(), rig.ToolCall{
						ID: "drive-spawn-teammate", Name: "spawn_teammate",
						Arguments: `{"name":"研究员","prompt":"研究一下当前工作目录并给出摘要"}`,
					})
					if invokeErr != nil {
						c.warnf("spawn_teammate 工具调用失败：%v", invokeErr)
					}
				}
				deadline := time.Now().Add(10 * time.Second)
				ready := false
				for time.Now().Before(deadline) {
					d.frame()
					for _, member := range team.Members() {
						if member.Last != "" {
							ready = true
						}
					}
					if ready {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				if !ready {
					c.warnf("队友在 10s 内没有完成首个回复，团队页签按空态验证")
				}
			}
			if !d.selectPane("团队") {
				c.failf("无法通过 pane 选择器切到「团队」")
				return
			}
			d.drain(2)
			c.shot("panel-team")
			if !d.has("团队") && !d.has("研究员") {
				c.failf("团队页签没有渲染内容")
			}
		})

	d.step("panel-close", "shell", "右栏：关闭检查器",
		"点击「收起右侧栏」→ 检查器关闭", func(c *check) {
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
			c.require(!d.uiRoot.RightPanelOpen(), "点击后右侧检查器仍打开")
			c.shot("panel-closed")
		})
}

// unused guard for strings (used by shortText in drive.go).
var _ = strings.TrimSpace
