package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// This file owns the two DSH-aligned session-control slash commands:
//
//	/goal    create, inspect and control the session goal (the backend is
//	         Controller.GoalAction, which persists through the goal store and
//	         arms/disarms the round driver);
//	/compact compact the current conversation on demand (the backend is the
//	         runtime agent's manual compaction API, which replaces the next
//	         turn's prior with a compacted surface).
//
// Both run locally: the slash line never becomes a model message, and every
// expected failure is rendered as a stable toast rather than an internal error
// string.

// goalControlWords are the /goal control words that must fill the whole
// argument list to act as commands, following DSH's syntax rule: any non-empty
// suffix turns the input into an objective, so "/goal pause after verification"
// sets the literal objective "pause after verification" instead of pausing.
var goalControlWords = map[string]string{
	"pause":    "pause",
	"resume":   "resume",
	"complete": "complete",
	"abandon":  "abandon",
	"clear":    "clear",
}

// runGoalCommand implements /goal [<objective> | edit <objective> | pause |
// resume | complete | abandon | clear].
func (a *App) runGoalCommand(args string) {
	args = strings.TrimSpace(args)
	if a.Ctrl == nil {
		a.toastText("Goal commands need a live session controller.",
			"目标命令需要实时会话控制器。")
		return
	}
	if strings.TrimSpace(a.Ctrl.CurrentSessionID()) == "" {
		a.toastText("Open a session before using /goal.",
			"请先打开会话再使用 /goal。")
		return
	}
	if args == "" {
		a.showGoalStatus()
		return
	}

	lower := strings.ToLower(args)
	if action, ok := goalControlWords[lower]; ok {
		a.runGoalControl(action)
		return
	}
	if lower == "edit" {
		a.toastText("Usage: /goal edit <objective>.",
			"用法：/goal edit <目标>。")
		return
	}
	if strings.HasPrefix(lower, "edit ") {
		objective := strings.TrimSpace(args[len("edit"):])
		if objective == "" {
			a.toastText("Usage: /goal edit <objective>.",
				"用法：/goal edit <目标>。")
			return
		}
		a.runGoalEdit(objective)
		return
	}
	// Anything else is the objective, including a control word followed by
	// more text.
	a.runGoalSet(args)
}

// showGoalStatus reports the current goal (objective, phase and automatic
// continuation rounds) and the commands that can act on it; without a goal it
// reports the usage. It never mutates the goal.
func (a *App) showGoalStatus() {
	goal := a.Ctrl.Goal()
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		a.toastText(
			"No goal set. Use /goal <objective> to create one, then /goal edit <objective>, /goal pause, /goal resume, /goal complete, /goal abandon or /goal clear.",
			"尚未设置目标。用 /goal <目标> 创建，之后可用 /goal edit <目标>、/goal pause、/goal resume、/goal complete、/goal abandon 或 /goal clear。")
		return
	}
	phase := strings.TrimSpace(string(goal.Phase))
	if phase == "" {
		phase = strings.TrimSpace(string(goal.Status))
	}
	rounds := a.Ctrl.GoalRounds(a.Ctrl.CurrentSessionID())
	a.toastText(
		fmt.Sprintf("Goal: %s · phase: %s · %d automatic continuation round(s). Next: /goal pause|resume, /goal edit <objective>, /goal complete, /goal abandon, /goal clear.",
			strings.TrimSpace(goal.Objective), phase, rounds),
		fmt.Sprintf("目标：%s · 阶段：%s · 已自动续轮 %d 次。下一步：/goal pause|resume、/goal edit <目标>、/goal complete、/goal abandon、/goal clear。",
			strings.TrimSpace(goal.Objective), phase, rounds))
}

// applyGoalAction applies one /goal lifecycle action through the controller
// and renders a stable error toast when the domain rejects it. A nil error
// with a nil goal is valid for "clear" (and for "get" without a goal).
func (a *App) applyGoalAction(action, objective string) error {
	if _, err := a.Ctrl.GoalAction(action, objective, nil, nil); err != nil {
		a.toastText("Could not update the goal. Use /goal to see the current state.",
			"无法更新目标。可用 /goal 查看当前状态。")
		return err
	}
	return nil
}

// runGoalSet creates a goal (or replaces the objective of a finished one).
func (a *App) runGoalSet(objective string) {
	objective = strings.TrimSpace(objective)
	if a.applyGoalAction("set", objective) != nil {
		return
	}
	a.toastText("Goal set: "+objective, "已设置目标："+objective)
}

// runGoalEdit replaces the current objective in place through the goal domain's
// edit action: the goal id, phase/status and automatic-continuation state are
// untouched, only the objective (and revision) change.
func (a *App) runGoalEdit(objective string) {
	objective = strings.TrimSpace(objective)
	if a.applyGoalAction("edit", objective) != nil {
		return
	}
	a.toastText("Goal updated: "+objective, "已更新目标："+objective)
}

// runGoalControl runs one lifecycle control word (pause, resume, complete,
// abandon, clear).
func (a *App) runGoalControl(action string) {
	if a.applyGoalAction(action, "") != nil {
		return
	}
	switch action {
	case "pause":
		a.toastText("Goal paused", "目标已暂停")
	case "resume":
		a.toastText("Goal resumed", "目标已恢复")
	case "complete":
		a.toastText("Goal completed", "目标已完成")
	case "abandon":
		a.toastText("Goal abandoned", "目标已放弃")
	case "clear":
		a.toastText("Goal cleared", "目标已清除")
	default:
		a.showGoalStatus()
	}
}

// runCompactCommand implements /compact (no arguments): compact the current
// session's conversation now and report what changed.
func (a *App) runCompactCommand(args string) {
	if strings.TrimSpace(args) != "" {
		a.toastText("Usage: /compact (no arguments).",
			"用法：/compact（不接受参数）。")
		return
	}
	if a.Ctrl == nil {
		a.toastText("Compaction needs a live session controller.",
			"压缩需要实时会话控制器。")
		return
	}
	if strings.TrimSpace(a.Ctrl.CurrentSessionID()) == "" {
		a.toastText("Open a session before compacting.",
			"请先打开会话再压缩。")
		return
	}
	replaced, saved, err := a.Ctrl.CompactCurrent(context.Background())
	if err != nil {
		a.toastCompactError(err)
		return
	}
	a.toastText(
		fmt.Sprintf("Compacted %d history items, about %d tokens saved.", replaced, saved),
		fmt.Sprintf("已压缩 %d 条历史，约节省 %d tokens。", replaced, saved))
}

// toastCompactError maps the classified compaction failures to stable,
// user-facing messages. Unexpected internal details stay out of the toast.
func (a *App) toastCompactError(err error) {
	switch {
	case errors.Is(err, session.ErrTurnRunning), errors.Is(err, runtime.ErrCompactionBusy):
		a.toastText("Compaction is unavailable because a turn is running; try again when the session is idle.",
			"当前有回合在运行，暂时无法压缩；请等会话空闲后重试。")
	case errors.Is(err, runtime.ErrNoCompactor):
		a.toastText("Compaction is not available because no compactor is configured.",
			"未配置压缩器，无法执行压缩。")
	case errors.Is(err, runtime.ErrNoCompactableHistory):
		a.toastText("Nothing to compact yet.", "暂无可压缩的历史。")
	case errors.Is(err, runtime.ErrCompactionNotSaved):
		a.toastText("Compaction finished, but the session could not be saved.",
			"压缩已完成，但会话保存失败。")
	case errors.Is(err, runtime.ErrCompactionFailed):
		a.toastText("Compaction could not produce a useful summary.",
			"压缩未能生成有效的摘要。")
	case errors.Is(err, session.ErrNoAgent):
		a.toastText("Compaction needs a live session agent.",
			"压缩需要可用的会话 Agent。")
	case errors.Is(err, session.ErrInvalidArgument):
		a.toastText("Open a session before compacting.",
			"请先打开会话再压缩。")
	case errors.Is(err, session.ErrClosed):
		a.toastText("The session controller is closed.",
			"会话控制器已关闭。")
	default:
		a.toastText("Compaction did not finish cleanly; inspect the session state before retrying.",
			"压缩未正常完成；重试前请检查当前会话状态。")
	}
}
