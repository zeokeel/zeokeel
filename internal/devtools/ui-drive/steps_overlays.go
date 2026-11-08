package main

// Overlay steps: the five floating interactions (approval, ask_user, present,
// plan_review, goal dialog) are triggered through the real host bridges and
// operated by clicking/Enter; the button order is asserted against the desktop
// convention (cancel/danger left, primary action rightmost).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func (d *drive) runOverlaySteps() {
	d.step("overlay-approval", "session", "浮层：审批对话框（真实工具调用）",
		"发送触发 working_directory 工具调用 → 审批对话框出现；按钮顺序为 拒绝（左）…允许（右）", func(c *check) {
			d.server.SetMode(scriptToolCallThenAnswer)
			defer d.server.SetMode(scriptAnswer)
			d.resetToSession()
			before, err := d.sendFromComposer("看看当前工作目录")
			if err != nil {
				c.failf("%v", err)
				return
			}
			if !d.waitText("需要审批", 10*time.Second) {
				c.failf("工具调用后审批对话框未出现")
				d.resolvePendingApprovals(contracts.ApprovalAllow)
				_, _ = d.waitTurn(before, 10*time.Second)
				return
			}
			c.shot("overlay-approval")
			c.require(c.has("本次运行内始终允许") || c.has("总是允许"),
				"审批对话框缺少「本次运行内始终允许」动作")
			allow, okAllow := d.find("允许")
			always, okAlways := d.findAny("本次运行内始终允许", "总是允许")
			deny, okDeny := d.find("拒绝")
			if !okAllow || !okDeny {
				c.failf("审批对话框按钮缺失（允许=%v 拒绝=%v）", okAllow, okDeny)
				d.resolvePendingApprovals(contracts.ApprovalAllow)
				_ = d.waitIdle(10 * time.Second)
				return
			}
			alwaysX := float32(-1)
			if okAlways {
				alwaysX = always.X
			}
			c.note("按钮 x：拒绝=%.0f 本次运行内始终允许=%.0f 允许=%.0f", deny.X, alwaysX, allow.X)
			if !(deny.X < allow.X) {
				c.failf("审批按钮顺序反桌面惯例：拒绝 x=%.0f 在允许 x=%.0f 的右边", deny.X, allow.X)
			}
			if okAlways && allow.X < always.X {
				c.failf("主操作「允许」不在最右：允许 x=%.0f，本次运行内始终允许 x=%.0f", allow.X, always.X)
			}
			// The safe default is visible in the copy.
			c.require(c.has("默认焦点") || c.has("Enter 执行当前聚焦的按钮") || c.has("Esc = 拒绝"),
				"审批对话框没有可见的安全默认说明（拒绝/Enter 语义）")
			// The empty-arguments rendering: {} shows 「无参数」 and no empty box.
			c.require(c.has("无参数"), "审批对话框对空参数 {} 没有显示「无参数」")
			// Clean up: approve (through the dialog when possible) and let the
			// turn finish.
			if !c.click("允许") {
				d.resolvePendingApprovals(contracts.ApprovalAllow)
			}
			_, _ = d.waitTurn(before, 15*time.Second)
			d.drain(2)
		})

	d.step("approval-safe-default", "session", "审批：安全默认（Enter 不放行）",
		"审批对话框默认焦点在「拒绝」；Enter 不会放行工具调用（拒绝/无动作），点「允许」才放行", func(c *check) {
			d.server.SetMode(scriptToolCallThenAnswer)
			defer d.server.SetMode(scriptAnswer)
			d.resetToSession()
			approvals, err := d.root.Require(contracts.KeyApprovals)
			if err != nil {
				c.failf("require approvals: %v", err)
				return
			}
			type decisionResult struct {
				decision contracts.ApprovalDecision
				err      error
			}
			decisions := make(chan decisionResult, 1)
			approvalCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			go func() {
				decision, reqErr := approvals.Request(approvalCtx, contracts.ApprovalRequest{
					ID: "drive-safe-default", Tool: "bash", Summary: "运行测试命令",
					Arguments: `{"command":"echo hi"}`,
				})
				decisions <- decisionResult{decision: decision, err: reqErr}
			}()
			if !d.waitText("需要审批", 10*time.Second) {
				c.failf("审批对话框未出现")
				return
			}
			d.drain(2)
			// The safe default: keyboard focus starts on Deny, never Allow.
			focusedDeny := d.tester.Focused("拒绝")
			focusedAllow := d.tester.Focused("允许")
			c.note("焦点：拒绝=%v 允许=%v", focusedDeny, focusedAllow)
			c.require(!focusedAllow, "审批对话框默认焦点落在「允许」上（不安全）")
			c.require(focusedDeny, "审批对话框默认焦点不在「拒绝」上（安全默认缺失）")
			c.require(c.has("Enter 执行当前聚焦的按钮") || c.has("Enter = 拒绝"),
				"审批对话框缺少默认焦点语义说明")
			// A shell-like tool's summary is shown as a 命令 block first, with
			// the raw arguments collapsed behind 「显示原始参数」.
			c.require(c.has("命令") || c.has("摘要"), "审批对话框没有把工具 Summary 作为命令/摘要块显示")
			c.require(c.has("显示原始参数"), "审批对话框没有折叠的「显示原始参数」入口")
			c.shot("approval-safe-default")
			// Enter must not approve the call.
			d.key(0, ui.KeyEnter)
			decided := decisionResult{}
			resolved := false
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				select {
				case decided = <-decisions:
					resolved = true
				default:
				}
				if resolved || !d.has("需要审批") {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if resolved {
				c.require(decided.decision != contracts.ApprovalAllow,
					"Enter 放行了工具调用（decision=%q）", decided.decision)
				c.note("Enter 的决议=%q（安全）", decided.decision)
				// Resolve the pending turn so the chat flow can continue.
				d.resolvePendingApprovals(contracts.ApprovalAllow)
			} else if d.has("需要审批") {
				c.note("Enter 未触发任何决议（对话框仍等待）")
				if !c.click("拒绝") {
					return
				}
				select {
				case decided = <-decisions:
					c.require(decided.decision == contracts.ApprovalDeny,
						"点击拒绝后决议=%q", decided.decision)
				case <-time.After(3 * time.Second):
					c.failf("点击拒绝后审批未返回")
				}
			} else {
				// The dialog closed on Enter; the decision must not be Allow.
				select {
				case decided = <-decisions:
					c.require(decided.decision != contracts.ApprovalAllow,
						"Enter 关闭对话框并放行了工具调用（decision=%q）", decided.decision)
				case <-time.After(3 * time.Second):
					c.failf("按 Enter 后对话框关闭但审批未返回决议")
				}
				d.resolvePendingApprovals(contracts.ApprovalAllow)
			}
			d.drain(2)
		})

	d.step("overlay-ask", "session", "浮层：ask_user 对话框",
		"UserInteraction.Ask → 问题对话框出现；点选项返回答案；Esc 取消返回错误", func(c *check) {
			d.resetToSession()
			interaction := d.uiRoot.UserInteraction()
			type askResult struct {
				answer string
				err    error
			}
			ask := func() (<-chan askResult, bool) {
				results := make(chan askResult, 1)
				askCtx, cancelAsk := context.WithTimeout(context.Background(), 30*time.Second)
				_ = cancelAsk
				go func() {
					answer, err := interaction.Ask(askCtx,
						"要使用哪个数据库？", []string{"SQLite（本地文件）", "PostgreSQL（服务端）"})
					results <- askResult{answer: answer, err: err}
				}()
				if !d.waitText("要使用哪个数据库？", 5*time.Second) {
					return nil, false
				}
				return results, true
			}
			results, ok := ask()
			if !ok {
				c.failf("ask_user 对话框未出现")
				return
			}
			c.shot("overlay-ask")
			option, okOption := d.find("SQLite（本地文件）")
			cancel, okCancel := d.find("取消")
			if !okOption {
				c.failf("ask 对话框没有选项按钮")
				return
			}
			// Options are full-width equal-choice rows; the cancel action sits
			// in the dialog footer. The desktop order rule applies when they
			// share a row (cancel/danger left, primary right).
			if okCancel {
				c.note("取消 rect=(%.0f,%.0f) 选项 rect=(%.0f,%.0f)", cancel.X, cancel.Y, option.X, option.Y)
				if cancel.Y+cancel.H > option.Y && cancel.Y < option.Y+option.H {
					if !(cancel.X < option.X) {
						c.failf("ask 按钮顺序反桌面惯例：取消 x=%.0f 在选项 x=%.0f 的右边", cancel.X, option.X)
					}
				} else {
					c.note("取消在 footer，选项为整行按钮：按「等权选项 + 次操作」布局接受")
				}
			} else {
				c.warnf("ask 对话框没有「取消」按钮")
			}
			if !c.click("SQLite（本地文件）") {
				return
			}
			select {
			case result := <-results:
				c.require(result.err == nil && result.answer == "SQLite（本地文件）",
					"选择选项后 Ask 返回 (%q, %v)", result.answer, result.err)
			case <-time.After(3 * time.Second):
				c.failf("选择选项后 Ask 未返回")
			}
			d.drain(2)
			c.shot("overlay-ask-answered")

			// A second question must be cancellable with Escape.
			results, ok = ask()
			if !ok {
				c.failf("第二次 ask_user 对话框未出现")
				return
			}
			d.key(0, ui.KeyEscape)
			select {
			case result := <-results:
				c.require(result.err != nil, "Esc 后 Ask 未返回取消错误（answer=%q）", result.answer)
			case <-time.After(3 * time.Second):
				c.failf("Esc 后 Ask 未返回")
			}
			d.drain(2)
			c.require(!d.has("要使用哪个数据库？"), "Esc 后问题对话框仍显示")
		})

	d.step("overlay-present", "session", "浮层：present 产物预览",
		"ArtifactPresenter.Present → 产物预览浮层出现；点「关闭」后消失", func(c *check) {
			d.resetToSession()
			presenter := d.uiRoot.ArtifactPresenter()
			_, err := presenter.Present(context.Background(), "检查报告",
				"# 构建结果\n\n- 30 个测试包通过\n- 0 失败\n")
			if err != nil {
				c.failf("Present 失败：%v", err)
				return
			}
			if !d.waitText("检查报告", 5*time.Second) {
				c.failf("present 产物预览浮层未出现")
				return
			}
			c.shot("overlay-present")
			if !c.has("关闭") {
				c.failf("产物预览浮层没有「关闭」按钮")
				return
			}
			if !c.click("关闭") {
				return
			}
			d.drain(2)
			c.require(!d.has("检查报告"), "关闭后产物预览仍显示")
		})

	d.step("overlay-plan", "session", "浮层：plan_review 计划审阅",
		"PlanReviewer.Review → 审阅浮层；取消计划在左、批准在最右；点「批准」才批准（空反馈 Enter 不放行）", func(c *check) {
			d.resetToSession()
			reviewer := d.uiRoot.PlanReviewer()
			go func() {
				_, _ = reviewer.Review(context.Background(),
					"# 发布计划\n\n1. 运行全量测试\n2. 更新文档与截图\n3. 打 tag 并发布")
			}()
			if !d.waitText("计划待审阅", 5*time.Second) {
				if !d.waitText("发布计划", 2*time.Second) {
					c.failf("plan_review 浮层未出现")
					return
				}
			}
			c.shot("overlay-plan")
			cancel, okCancel := d.find("取消计划")
			if !okCancel {
				cancel, okCancel = d.find("取消")
			}
			approve, okApprove := d.find("批准")
			if !okApprove {
				c.failf("计划审阅浮层没有「批准」按钮")
				_, _ = reviewer.Exit("cancel")
				return
			}
			if okCancel {
				c.note("按钮 x：取消计划=%.0f 批准=%.0f", cancel.X, approve.X)
				if !(cancel.X < approve.X) {
					c.failf("计划审阅按钮顺序反桌面惯例：取消计划 x=%.0f 在批准 x=%.0f 的右边", cancel.X, approve.X)
				}
			}
			c.require(c.has("点击「批准」批准计划") || c.has("批准计划"),
				"计划审阅缺少「点击批准」的静态提示")
			c.require(!c.has("Enter = 批准") && !c.has("Enter=批准"),
				"计划审阅底部提示仍写「Enter = 批准」（显式批准裁决要求去掉）")
			// Esc must not cancel the plan: the dialog stays open, reviewing.
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(d.has("计划待审阅"), "Esc 关闭了计划审阅（应保持打开）")
			c.require(reviewer.State() == "reviewing", "Esc 后计划状态=%q（应为 reviewing）", reviewer.State())
			// Enter with empty feedback must NOT approve; only the explicit
			// button does.
			d.key(0, ui.KeyEnter)
			deadline := time.Now().Add(800 * time.Millisecond)
			for time.Now().Before(deadline) {
				d.frame()
				if reviewer.State() == "approved" {
					c.failf("空反馈按 Enter 批准了计划（应显式点击批准）")
					c.shot("overlay-plan-enter-fail")
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			c.note("空反馈 Enter 未批准计划（符合安全语义）")
			if !c.click("批准") {
				return
			}
			deadline = time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && reviewer.State() != "approved" {
				d.frame()
				time.Sleep(10 * time.Millisecond)
			}
			c.require(reviewer.State() == "approved", "点击批准后审核状态=%q", reviewer.State())
			d.drain(2)
			c.shot("overlay-plan-approved")
		})

	d.step("overlay-plan-feedback", "session", "浮层：计划反馈 Enter = 继续规划",
		"计划审阅反馈框输入文本后 Enter = 继续规划（取消审阅并把反馈发给 agent），不是批准", func(c *check) {
			d.server.SetMode(scriptAnswer)
			d.resetToSession()
			reviewer := d.uiRoot.PlanReviewer()
			go func() {
				_, _ = reviewer.Review(context.Background(),
					"# 反馈计划\n\n1. 第一步\n2. 第二步")
			}()
			if !d.waitText("计划待审阅", 5*time.Second) {
				c.failf("计划审阅浮层未出现")
				return
			}
			c.shot("plan-before-feedback")
			// The hint advertises the keep-planning Enter path and never a
			// dialog-wide Enter=approve.
			c.require(c.has("继续规划") && c.has("批准"),
				"计划审阅提示/按钮缺少「继续规划」与「批准」")
			c.require(!c.has("Enter = 批准") && !c.has("Enter=批准"),
				"计划审阅底部提示仍写「Enter = 批准」")
			if !c.click("继续规划反馈") {
				if !c.click("告诉 agent 计划需要怎么改…") {
					c.failf("找不到计划反馈输入框")
					_, _ = reviewer.Exit("cancel")
					return
				}
			}
			d.typeText("请补充边界测试")
			d.drain(2)
			c.require(c.has("发送反馈给 agent") || c.has("继续规划（发送反馈"),
				"输入反馈后提示文案未切换为「Enter = 继续规划」")
			chatBefore := len(d.userMessages())
			d.key(0, ui.KeyEnter)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				if len(d.userMessages()) > chatBefore || !d.has("计划待审阅") {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if reviewer.State() == "approved" {
				c.failf("反馈框 Enter 批准了计划（应为继续规划）")
				c.shot("plan-feedback-approved-fail")
				return
			}
			c.require(reviewer.State() == "cancelled" || reviewer.State() == "reviewing",
				"反馈 Enter 后审阅状态=%q", reviewer.State())
			if len(d.userMessages()) <= chatBefore {
				c.failf("继续规划没有把反馈发送给 agent（用户消息数未增加）")
			} else {
				last := d.userMessages()[len(d.userMessages())-1]
				c.require(strings.Contains(last.Text, "请补充边界测试"),
					"继续规划的反馈消息内容=%q", shortText(last.Text))
			}
			c.require(c.has("已发送继续规划反馈"), "继续规划后没有「已发送继续规划反馈」toast")
			d.drain(2)
			c.shot("plan-feedback-sent")
			d.syncSession()
			// Let the agent turn finish (scripted answer).
			deadline = time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				d.frame()
				session, ok := d.chat().Session(d.sessionID)
				if ok && !session.Running && len(session.Messages) > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		})

	d.step("overlay-goal", "session", "浮层：goal 目标（chip 菜单 + 目标对话框）",
		"GoalTracker.Set → 头部出现目标 chip；点 chip 打开动作菜单（暂停/完成/放弃/清除）；/goal 对话框取消在左/保存在右", func(c *check) {
			d.resetToSession()
			tracker := d.uiRoot.GoalTracker()
			if _, err := tracker.Set(context.Background(), "完成 Q4 发布清单并更新文档"); err != nil {
				c.failf("GoalTracker.Set 失败：%v", err)
				return
			}
			d.drain(3)
			chip := d.goalChipLabel()
			if chip == "" {
				c.failf("设置目标后头部没有目标 chip")
				return
			}
			c.note("目标 chip 标签=%q", chip)
			c.shot("overlay-goal-header")
			// The chip opens the goal actions popover.
			if !c.click(chip) {
				return
			}
			d.drain(2)
			if !c.has("暂停目标") && !c.has("继续目标") {
				c.failf("目标 chip 没有打开目标动作菜单")
				return
			}
			c.shot("overlay-goal-menu")
			// H-series goal semantics: ending keeps the record, deleting removes
			// it — two distinct menu rows, not two adjacent danger siblings.
			c.require(c.has("结束目标（保留记录）"), "目标菜单缺少「结束目标（保留记录）」")
			c.require(c.has("删除目标记录"), "目标菜单缺少「删除目标记录」")
			if c.has("暂停目标") {
				if !c.click("暂停目标") {
					return
				}
				d.drain(2)
				if _, _, status, ok := tracker.Get(); !ok || status != contracts.GoalStatusPaused {
					c.failf("暂停目标后 GoalTracker 状态=%q（ok=%v）", status, ok)
				}
				if !c.has("目标已暂停") {
					c.warnf("暂停目标后 chip 没有显示「目标已暂停」（当前 chip=%q）", d.goalChipLabel())
				}
				c.shot("overlay-goal-paused")
				// Resume through the same chip menu (its label changed).
				if resumed := d.goalChipLabel(); resumed != "" {
					if !c.click(resumed) {
						return
					}
					d.drain(2)
					if c.has("继续目标") {
						if !c.click("继续目标") {
							return
						}
						d.drain(2)
					}
				}
			}
			// The /goal dialog is reachable through the slash command surface.
			if err := d.focusComposer(); err != nil {
				c.failf("%v", err)
				return
			}
			d.clearDraft()
			d.typeText("/goal")
			d.drain(2)
			if !c.has("设置目标") {
				c.failf("斜杠菜单里没有「设置目标」命令")
				d.clearDraft()
				return
			}
			if !c.click("设置目标") {
				d.clearDraft()
				return
			}
			d.drain(2)
			c.mustText("删除目标记录", "保存", "取消")
			save, okSave := d.find("保存")
			cancel, okCancel := d.find("取消")
			if okSave && okCancel {
				c.note("目标对话框按钮 x：取消=%.0f 保存=%.0f", cancel.X, save.X)
				if !(cancel.X < save.X) {
					c.failf("目标对话框按钮顺序反桌面惯例：取消 x=%.0f 在保存 x=%.0f 的右边", cancel.X, save.X)
				}
			}
			c.shot("overlay-goal-dialog")
			d.key(0, ui.KeyEscape)
			d.drain(2)
			c.require(!c.has("删除目标记录"), "Esc 未关闭目标对话框")
			if _, _, _, ok := tracker.Get(); !ok {
				c.failf("关闭目标对话框后目标被意外清除")
			}
			tracker.Clear()
			d.drain(2)
		})

}

// resolvePendingApprovals answers every pending request (cleanup path when the
// UI did not show the dialog).
func (d *drive) resolvePendingApprovals(decision contracts.ApprovalDecision) {
	approvals, err := d.root.Require(contracts.KeyApprovals)
	if err != nil {
		fmt.Printf("ui-drive: require approvals: %v\n", err)
		return
	}
	for _, pending := range approvals.Pending() {
		_ = approvals.Resolve(pending.ID, decision)
	}
}

// waitApprovalsCleared pumps frames until no approval is pending.
func (d *drive) waitApprovalsCleared(timeout time.Duration) bool {
	approvals, err := d.root.Require(contracts.KeyApprovals)
	if err != nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.frame()
		if len(approvals.Pending()) == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
