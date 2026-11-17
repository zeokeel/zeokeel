package main

// Session-composer steps: slash menu, @ menu, composer popover menus, sending
// a prompt through the scripted model, and the running/Stop state.

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (d *drive) runSessionSteps() {
	d.step("slash-open", "session", "斜杠菜单：输入 / 出现",
		"聚焦输入框输入「/」→ 斜杠命令菜单出现（新会话/创建副本/导出对话/打开设置）", func(c *check) {
			d.resetToSession()
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.typeText("/")
			d.drain(2)
			c.mustText("打开设置", "新会话", "创建副本", "导出对话")
			c.shot("slash-open")
			d.clearDraft()
		})

	d.step("slash-keyboard", "session", "斜杠菜单：↑↓ + Enter 执行命令",
		"输入「/打开」→ 菜单出现 → ↓/↑ → Enter 执行「打开设置」，不把 /... 作为消息发送", func(c *check) {
			d.resetToSession()
			before := len(d.userMessages())
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.typeText("/打开")
			d.drain(2)
			c.require(d.has("打开设置"), "「/打开」后斜杠菜单没有「打开设置」项")
			d.key(0, ui.KeyDown)
			d.frame()
			d.key(0, ui.KeyUp)
			d.frame()
			d.key(0, ui.KeyEnter)
			// Watch a short window: the fixed composer runs the highlighted
			// command; the unfixed one sends the slash draft as a message.
			sent := false
			opened := false
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				if len(d.userMessages()) > before {
					sent = true
					break
				}
				if d.uiRoot.Navigator().SettingsOpen() {
					opened = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if sent {
				last := d.userMessages()[len(d.userMessages())-1]
				c.failf("Enter 把斜杠命令当消息发送了（user message=%q）", shortText(last.Text))
				_ = d.waitIdle(10 * time.Second)
				d.clearDraft()
				return
			}
			if !opened {
				c.failf("Enter 未运行「打开设置」命令（设置未打开）")
				d.clearDraft()
				return
			}
			c.note("Enter 执行了命令而非发送消息（用户消息数未变）")
			c.shot("slash-command-ran")
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
		})

	d.step("slash-esc", "session", "斜杠菜单：Esc 关闭且保留草稿",
		"输入「/」→ Esc 关闭菜单，草稿「/」保留（发送内容为 / 而非丢失）", func(c *check) {
			d.resetToSession()
			before := len(d.userMessages())
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.typeText("/")
			d.drain(2)
			c.require(d.has("打开设置"), "斜杠菜单未出现")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			menuClosed := !d.has("打开设置")
			c.require(menuClosed, "Esc 未关闭斜杠菜单（菜单项仍显示）")
			if !menuClosed {
				d.clearDraft()
				return
			}
			// The draft must still hold "/": sending it produces that message.
			if _, err := d.clickSend(); err != nil {
				c.failf("%v", err)
				return
			}
			if _, err := d.waitTurn(before, 15*time.Second); err != nil {
				c.failf("%v", err)
				return
			}
			users := d.userMessages()
			if len(users) != before+1 {
				c.failf("Esc 后草稿丢失（用户消息数 %d → %d）", before, len(users))
				return
			}
			last := users[len(users)-1]
			c.require(strings.TrimSpace(last.Text) == "/",
				"Esc 后草稿内容=%q，期望 \"/\"", shortText(last.Text))
			c.shot("slash-esc-draft")
		})

	d.step("mention-open", "session", "＠菜单：输入 @ 出现文件候选",
		"聚焦输入框输入「@REA」→ 出现 /README.md 文件候选", func(c *check) {
			d.resetToSession()
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.typeText("@REA")
			d.drain(3)
			c.require(d.has("/README.md"), "「@REA」后没有出现 /README.md 候选")
			c.shot("mention-open")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
		})

	d.step("mention-esc-enter", "session", "＠菜单：Esc 关闭 / Enter 选择",
		"「@REA」→ Esc 关闭菜单且不发送；再次输入 → Enter 选择候选而不会当消息发出", func(c *check) {
			d.resetToSession()
			before := len(d.userMessages())
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.typeText("@RE")
			d.drain(3)
			c.require(d.has("/README.md"), "「@RE」后没有出现 /README.md 候选")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			escClosed := !d.has("/README.md")
			if !escClosed {
				c.failf("Esc 未关闭 @ 菜单")
				d.clearDraft()
				return
			}
			// Re-open with a fresh draft (Esc remembers the dismissed draft;
			// use a different query so the dismissal cannot suppress the menu)
			// and choose the first suggestion with Enter.
			d.clearDraft()
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.typeText("@REA")
			d.drain(3)
			d.key(0, ui.KeyEnter)
			// The fixed menu inserts the reference; the unfixed one sends the
			// "@..." draft as a chat message.
			sent := false
			inserted := false
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				if len(d.userMessages()) > before {
					sent = true
					break
				}
				if !d.has("/README.md") {
					inserted = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if sent {
				last := d.userMessages()[len(d.userMessages())-1]
				c.failf("Enter 把 @ 引用当消息发送了（user message=%q）", shortText(last.Text))
				_ = d.waitIdle(10 * time.Second)
			} else {
				c.require(inserted, "Enter 后 @ 菜单未关闭也未插入引用")
			}
			c.shot("mention-enter")
			d.clearDraft()
		})

	d.step("composer-model-menu", "session", "输入区：模型菜单",
		"点击模型 chip「drive-model」→ 窗口内菜单打开（选择模型/打开模型设置）→ Esc 关闭", func(c *check) {
			d.resetToSession()
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			if !c.click("drive-model") {
				return
			}
			d.drain(2)
			if !c.has("打开模型设置") {
				c.failf("点击模型 chip 后没有出现模型菜单")
				return
			}
			// The first level is the compact two-entry menu; the model list and
			// the reasoning levels live one level down (no stacked long menu).
			c.require(c.has("模型"), "模型 chip 菜单缺少「模型」二级入口")
			c.require(c.has("推理等级"), "模型 chip 菜单缺少「推理等级」二级入口")
			c.require(!c.has("选择模型"), "模型 chip 菜单仍是旧的堆叠式「选择模型」列表")
			c.note("菜单项可见：模型 / 推理等级 / 打开模型设置")
			c.shot("composer-model-menu")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(!c.has("打开模型设置"), "Esc 后模型菜单未关闭")
		})

	d.step("composer-permission-menu", "session", "输入区：权限按钮只显示权限模式",
		"工具栏按钮默认显示「工作区修改（全局）」（审计披露文案；不再是「会话设置」）→ 点击只显示权限三态（工作区修改 / 所有权限 / 只读）→ Esc 关闭", func(c *check) {
			d.resetToSession()
			c.require(c.has("工作区修改（全局）"), "权限按钮没有显示默认「工作区修改（全局）」")
			c.require(!c.has("会话设置"), "工具栏仍出现「会话设置」")
			if !c.click("工作区修改（全局）") {
				c.failf("composer 缺少权限按钮")
				return
			}
			d.drain(2)
			c.require(c.has("权限模式"), "权限菜单缺少权限模式分组")
			for _, want := range []string{"工作区修改", "所有权限", "只读"} {
				c.require(c.has(want), "权限菜单缺少权限「%s」", want)
			}
			for _, gone := range []string{"会话设置", "对话模式", "人设", "插入预设", "打开行为设置", "清除自动允许的工具"} {
				c.require(!c.has(gone), "权限菜单仍显示已移除项「%s」", gone)
			}
			c.require(!c.has("推理等级"), "权限菜单仍含推理等级（应只在模型 chip 菜单）")
			c.shot("composer-session-settings-menu")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			if c.has("权限模式") {
				d.key(0, ui.KeyEscape)
				d.drain(2)
			}
			c.require(!c.has("权限模式"), "Esc 后权限菜单仍未关闭")
			c.require(c.has("工作区修改（全局）"), "Esc 后权限按钮没有恢复当前权限文案")
		})

	d.step("composer-toolbar-groups", "session", "输入区：工具栏分组",
		"composer 动作行只有附件、模型 chip、一个权限按钮和右侧发送；权限按钮显示当前权限，模型/推理等级在模型 chip 的二级菜单里", func(c *check) {
			d.resetToSession()
			c.require(c.has("附件"), "composer 缺少附件菜单")
			c.require(c.has("drive-model"), "composer 缺少模型 chip")
			c.require(c.has("工作区修改（全局）"), "composer 权限按钮没有显示当前权限")
			c.require(!c.has("会话设置"), "composer 仍出现「会话设置」")
			rect, ok := d.find("发送")
			if !ok {
				c.failf("composer 缺少发送按钮")
				return
			}
			c.note("发送 rect=(%.0f,%.0f,%.0f,%.0f)", rect.X, rect.Y, rect.W, rect.H)
			if !c.click("工作区修改（全局）") {
				return
			}
			d.drain(2)
			for _, want := range []string{"权限模式", "工作区修改", "所有权限", "只读"} {
				c.require(c.has(want), "权限菜单缺少「%s」", want)
			}
			for _, gone := range []string{"会话设置", "对话模式", "插入预设", "人设", "打开行为设置", "清除自动允许的工具"} {
				c.require(!c.has(gone), "权限菜单仍显示已移除项「%s」", gone)
			}
			if header, ok := d.find("权限模式"); ok {
				if item, okItem := d.find("工作区修改"); okItem && item.Y <= header.Y {
					c.failf("权限模式分组标题不在模式项上方：header Y=%.1f item Y=%.1f", header.Y, item.Y)
				}
			}
			d.key(0, ui.KeyEscape)
			d.drain(2)
			// Narrow composer: the model entry stays on the row or behind the
			// official toolbar overflow; the session menu is still permission-only.
			d.setSize(900, 600)
			d.resetToSession()
			d.drain(3)
			c.require(d.has("附件"), "窄窗 composer 工具栏缺少附件菜单")
			c.require(d.has("工作区修改（全局）"), "窄窗 composer 权限按钮没有显示当前权限")
			c.require(d.has("发送"), "窄窗 composer 工具栏缺少发送按钮")
			if rect, ok := d.find("drive-model"); !ok || rect.W <= 0 {
				if !c.click("更多") && !c.click("More") {
					c.failf("窄窗 composer 找不到模型入口（toolbar 或 overflow）")
				} else {
					d.drain(2)
					c.require(d.has("drive-model") || d.has("test-mod"), "窄窗 overflow 缺少模型入口")
					d.key(0, ui.KeyEscape)
					d.drain(2)
				}
			}
			if c.click("工作区修改（全局）") {
				d.drain(2)
				c.require(d.has("权限模式"), "窄窗权限菜单缺少权限模式")
				c.require(!d.has("对话模式"), "窄窗权限菜单仍显示对话模式")
				d.key(0, ui.KeyEscape)
				d.drain(2)
			}
			c.shot("composer-toolbar-narrow")
			d.setSize(1280, 800)
			d.frame()
			c.shot("composer-toolbar")
		})

	d.step("composer-slash-keyboard", "session", "输入区：「/」命令菜单",
		"输入区不再有独立的「/」按钮：在输入框键入 / 即打开斜杠命令菜单 → Esc 关闭（键盘优先）", func(c *check) {
			d.resetToSession()
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.clearDraft()
			d.typeText("/")
			d.drain(2)
			if !d.has("打开设置") && !d.has("新会话") {
				c.failf("键入 / 没有打开斜杠命令菜单；texts=%v", paletteTailTexts(d))
				return
			}
			c.shot("composer-slash-menu")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			d.clearDraft()
		})

	d.step("send-answer", "session", "发送：输入文字→发送→收到回复",
		"输入提示词点击「发送」→ 会话进入运行→结束，助手回复文本显示", func(c *check) {
			d.server.SetMode(scriptAnswer)
			d.resetToSession()
			reply, err := d.sendMessage("请给一段总结", 20*time.Second)
			if err != nil {
				c.failf("%v", err)
				return
			}
			d.drain(3)
			c.require(strings.Contains(reply, "脚本回复"),
				"助手回复未包含脚本内容：%q", shortText(reply))
			c.require(d.has("脚本回复"), "会话界面没有显示助手回复文本")
			c.shot("send-answer")
		})

	d.step("stream-stop", "session", "流式：运行中「停止」可见并可点",
		"慢速流式回复时「停止」按钮可见 → 点击停止 → 会话回到空闲", func(c *check) {
			d.server.SetMode(scriptSlowAnswer)
			defer d.server.SetMode(scriptAnswer)
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
			if !d.waitRunning(true, 10*time.Second) {
				c.failf("发送后未观察到运行中的「停止」按钮")
				_, _ = d.waitTurn(before, 10*time.Second)
				return
			}
			c.note("运行中观察到「停止」按钮")
			c.shot("stream-running")
			if !c.click("停止") {
				return
			}
			if _, err := d.waitTurn(before, 15*time.Second); err != nil {
				c.failf("%v", err)
				return
			}
			d.drain(3)
			c.require(!d.hasExact("停止"), "停止后「停止」按钮仍显示")
			c.shot("stream-stopped")
		})
}
