package session

// Live work steps: while a turn runs, the standard/compact transcript shows one
// quiet activity line (what the agent is doing and, in standard mode, the step's
// live detail) instead of streaming the whole tool timeline into the thread. The
// detailed/verbose modes keep the timeline. Activity words follow the product's
// tool vocabulary (uikit.ToolDisplayName, contract tool ids) so a reader can tell
// a file read from a command run at a glance; the detail is the tool call's own
// summary, and the elapsed clock starts at the message's CreatedAt.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// workStepActivity is the live step's activity: the category glyph and the
// localized status words (the mapping table lives in workStepStatus).
type workStepActivity struct {
	icon *ui.SVG
	zh   string
	en   string
}

// liveWorkStepActivity resolves the activity of the turn's current step: the
// latest finished tool call names the work (its category glyph and status word),
// and a turn that has not called a tool yet is analyzing the request.
func liveWorkStepActivity(message contracts.ChatMessage) workStepActivity {
	if len(message.Tools) == 0 {
		return workStepActivity{icon: icons.Sparkles(), zh: "正在分析请求", en: "Analyzing the request"}
	}
	tool := message.Tools[len(message.Tools)-1]
	activity := workStepStatus(tool.Name)
	activity.icon = uikit.ToolIcon(tool.Name)
	return activity
}

// workStepStatus maps one tool id to its live activity words. The table mirrors
// the DSH vocabulary: an action verb for the family, not the tool's formal name.
func workStepStatus(name string) workStepActivity {
	key := strings.ToLower(strings.TrimSpace(name))
	switch {
	case key == "read_image":
		return workStepActivity{zh: "正在读取图片", en: "Reading an image"}
	case key == "read_file" || key == "list_dir" || key == "glob" ||
		strings.HasPrefix(key, "read_"):
		return workStepActivity{zh: "正在读取文件", en: "Reading files"}
	case key == "write_file":
		return workStepActivity{zh: "正在写入文件", en: "Writing files"}
	case key == "edit_file" || key == "str_replace_editor" || key == "apply_patch":
		return workStepActivity{zh: "正在编辑文件", en: "Editing files"}
	case key == "grep" || key == "search_files":
		return workStepActivity{zh: "正在搜索代码", en: "Searching the code"}
	case key == "bash" || key == "pwsh" || key == "shell" ||
		key == "run_command" || strings.HasPrefix(key, "terminal_"):
		return workStepActivity{zh: "正在运行命令", en: "Running a command"}
	case key == "run_code" || key == "execute_code" || key == "code_interpreter" ||
		strings.HasPrefix(key, "code_"):
		return workStepActivity{zh: "正在运行代码", en: "Running code"}
	case key == "web_search":
		return workStepActivity{zh: "正在搜索网页", en: "Searching the web"}
	case key == "web_fetch":
		return workStepActivity{zh: "正在访问网页", en: "Fetching a web page"}
	case key == "subagent" || key == "workflow" || key == "ralph" ||
		strings.HasPrefix(key, "team_"):
		return workStepActivity{zh: "正在协调子智能体", en: "Coordinating subagents"}
	case key == "plan_review" || key == "exit_plan_mode" ||
		key == "update_plan" || key == "todo":
		return workStepActivity{zh: "正在更新计划", en: "Updating the plan"}
	case key == "ask_user":
		return workStepActivity{zh: "等待你的操作", en: "Waiting for you"}
	default:
		return workStepActivity{zh: "正在调用工具", en: "Calling a tool"}
	}
}

// liveWorkStepDetail is the step's live detail: the latest tool call's summary,
// already normalized to one line (empty when the call reported none).
func liveWorkStepDetail(message contracts.ChatMessage) string {
	if len(message.Tools) == 0 {
		return ""
	}
	return uikit.ToolPreview(message.Tools[len(message.Tools)-1].Summary, 96)
}

// buildLiveWorkSteps draws the running turn's single activity line:
//
//	[activity icon] status word [· live detail]     用时 8.0s
//
// The detail is omitted in compact mode; the elapsed clock derives from the
// turn's CreatedAt and is omitted when the turn has no recorded start.
func buildLiveWorkSteps(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage, showDetail bool) {
	activity := liveWorkStepActivity(message)
	label := st.tr(activity.zh, activity.en)
	ui.Row(c.Key("session.live." + message.ID)).FillWidth().AlignItems(ui.Center).
		Gap(design.Space2).PaddingY(design.Space1).Children(func() {
		ui.Icon(c, activity.icon).Size(design.Space4, design.Space4).
			TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
			TextColor(t.LabelSecondary).NoWrap()
		if showDetail {
			if detail := liveWorkStepDetail(message); detail != "" {
				design.Apply(ui.Text(c, "·"), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).NoWrap()
				design.Apply(ui.Text(c, detail), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).Grow(1).MinWidthPercent(0).
					MaxLines(1).Ellipsis("…")
			} else {
				ui.Spacer(c).Grow(1)
			}
		} else {
			ui.Spacer(c).Grow(1)
		}
		if elapsed := liveWorkStepElapsed(c, message); elapsed != "" {
			design.Apply(ui.Text(c, st.tr("用时 ", "Took ")+elapsed),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		}
	})
}

// liveWorkStepElapsed is the running step's clock, from the turn's CreatedAt to
// the frame clock, in the shared compact duration format. A turn without a
// recorded start renders no clock.
func liveWorkStepElapsed(c *ui.Context, message contracts.ChatMessage) string {
	if message.CreatedAt.IsZero() {
		return ""
	}
	millis := c.Now().Sub(message.CreatedAt).Milliseconds()
	// A fresh turn reads as "用时 0ms" for its first frames; the clock only
	// appears once a whole second has passed (DSH shows the duration once it is
	// meaningful too).
	if millis < 1000 {
		return ""
	}
	return formatUsageDuration(millis)
}
