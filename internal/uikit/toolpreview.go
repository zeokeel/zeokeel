package uikit

// The tool call's preview copy and expanded-panel sections, as pure functions
// of one contracts.ToolCall: the transcript's tool rows (ToolRow/ToolCallCard)
// and any screen that renders a call build them here, in the shared layer, so
// no UI module imports another module's implementation for them (AGENTS.md §7
// module boundary; 2026-11 audit AR-01 moved them out of the toolcards module,
// which keeps its own copies until its renderers switch to these).
//
// ToolCallPreview is the collapsed row's one-line copy: the localized action
// line from the preview registry when the tool and its arguments provide one,
// else the backend result summary unchanged. ToolPanelSections is the expanded
// panel: the tool's input (command / path / before-after / pretty JSON) then
// its bounded output, each section copyable and honest about truncation.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The preview and panel bounds. toolPreviewAssumedSummaryRunes mirrors the
// agent's read-model summary bound: a summary at this length ending with the
// truncation ellipsis was cut by the backend. Keep the two in sync.
const (
	toolPreviewMaxRunes            = 96
	toolPreviewMaxInputRunes       = 4000
	toolPreviewMaxContentRunes     = 2000
	toolPreviewAssumedSummaryRunes = 4000
)

// toolPreviewTemplate renders one tool call's localized one-line preview from
// its arguments. It returns "" when the arguments do not carry the fields the
// line needs, and ToolCallPreview falls back to the backend result summary.
type toolPreviewTemplate func(locale contracts.LocaleService, call contracts.ToolCall) string

// toolPreviewTemplates is the collapsed tool row's preview registry. The
// backend result summary is raw English narration ("created shared team task
// id: task-1 subject: ...") that reads as protocol noise in a one-line row; a
// tool that ships a card gets the product's localized action line instead,
// built from the arguments it already received. Only registered tools with the
// template's fields use it: an unknown tool, or a known tool whose arguments
// lack the field, keeps the backend summary unchanged.
var toolPreviewTemplates = map[string]toolPreviewTemplate{
	// shell / files
	"bash":               toolPreviewLine("运行命令：%s", "Run command: %s", "command"),
	"pwsh":               toolPreviewLine("运行命令：%s", "Run command: %s", "command"),
	"read_file":          toolPreviewLine("读取文件：%s", "Read file: %s", "path"),
	"write_file":         toolPreviewLine("写入文件：%s", "Write file: %s", "path"),
	"edit_file":          toolPreviewLine("编辑文件：%s", "Edit file: %s", "path"),
	"str_replace_editor": toolPreviewLine("编辑文件：%s", "Edit file: %s", "path"),
	"list_dir":           toolPreviewLine("列出目录：%s", "List directory: %s", "path"),
	"glob":               toolPreviewLine("查找文件：%s", "Find files: %s", "pattern"),
	"grep":               toolPreviewLine("搜索代码：%s", "Search code: %s", "pattern"),
	"read_image":         toolPreviewLine("查看图片：%s", "View image: %s", "path"),
	// web
	"web_fetch":  toolPreviewLine("抓取网页：%s", "Fetch page: %s", "url"),
	"web_search": toolPreviewLine("搜索网页：%s", "Search the web: %s", "query", "queries"),
	// jobs / terminals
	"job_start":       toolPreviewLine("启动后台任务：%s", "Start background job: %s", "command"),
	"job_output":      toolPreviewLine("读取后台任务输出：%s", "Read job output: %s", "job_id"),
	"terminal_open":   toolPreviewLine("打开终端：%s", "Open terminal: %s", "cwd", "name", "shell"),
	"terminal_send":   toolPreviewTerminalSend,
	"terminal_read":   toolPreviewLine("读取终端输出：%s", "Read terminal output: %s", "session_id", "sessionId"),
	"terminal_signal": toolPreviewTerminalSignal,
	"terminal_close":  toolPreviewLine("关闭终端：%s", "Close terminal: %s", "session_id", "sessionId"),
	// orchestration / team tasks
	"subagent":         toolPreviewLine("启动子代理：%s", "Start subagent: %s", "description", "prompt"),
	"team_task_create": toolPreviewLine("创建团队任务：%s", "Create team task: %s", "subject"),
	"team_task_update": toolPreviewTeamTaskUpdate,
	"team_task_get":    toolPreviewLine("查看任务：%s", "View task: %s", "id"),
	"team_task_list":   toolPreviewTeamTaskList,
}

// ToolCallPreview renders the collapsed tool row's one-line copy for one call:
// the localized action line from the preview registry when the tool and its
// arguments provide one, else the backend result summary unchanged (the row's
// earlier behavior). The line is normalized to one line and bounded to
// toolPreviewMaxRunes.
func ToolCallPreview(locale contracts.LocaleService, call contracts.ToolCall) string {
	text := ""
	if template, ok := toolPreviewTemplates[strings.ToLower(strings.TrimSpace(call.Name))]; ok {
		text = template(locale, call)
	}
	if strings.TrimSpace(text) == "" {
		text = call.Summary
	}
	return ToolPreview(text, toolPreviewMaxRunes)
}

// toolPreviewLine builds the registry's common shape: one localized line filled
// with the first present argument among keys. It reports "" (so ToolCallPreview
// keeps the backend summary) when none of the fields carries a value.
func toolPreviewLine(zhFormat, enFormat string, keys ...string) toolPreviewTemplate {
	return func(locale contracts.LocaleService, call contracts.ToolCall) string {
		value := toolPreviewArgValue(call.Arguments, keys...)
		if value == "" {
			return ""
		}
		return fmt.Sprintf(T(locale, zhFormat, enFormat), value)
	}
}

// toolPreviewTerminalSend names the terminal and the text one terminal_send
// writes. The session argument accepts both the camelCase and the snake_case
// spelling the backend records.
func toolPreviewTerminalSend(locale contracts.LocaleService, call contracts.ToolCall) string {
	session := toolPreviewArgValue(call.Arguments, "session_id", "sessionId")
	text := toolPreviewArgValue(call.Arguments, "text", "input")
	if session == "" || text == "" {
		return ""
	}
	return fmt.Sprintf(T(locale, "向终端 %s 发送：%s", "Send to terminal %s: %s"), session, text)
}

// toolPreviewTerminalSignal names the terminal and the signal sent to it.
func toolPreviewTerminalSignal(locale contracts.LocaleService, call contracts.ToolCall) string {
	session := toolPreviewArgValue(call.Arguments, "session_id", "sessionId")
	signal := toolPreviewArgValue(call.Arguments, "signal")
	if session == "" || signal == "" {
		return ""
	}
	return fmt.Sprintf(T(locale, "向终端 %s 发送信号：%s", "Signal terminal %s: %s"), session, signal)
}

// toolPreviewTeamTaskUpdate names the task and the localized board action.
func toolPreviewTeamTaskUpdate(locale contracts.LocaleService, call contracts.ToolCall) string {
	id := toolPreviewArgValue(call.Arguments, "id")
	if id == "" {
		return ""
	}
	action := toolPreviewArgValue(call.Arguments, "action")
	if action == "" {
		return fmt.Sprintf(T(locale, "更新任务 %s", "Update task %s"), id)
	}
	return fmt.Sprintf(T(locale, "更新任务 %s（%s）", "Update task %s (%s)"),
		id, toolPreviewTaskActionLabel(locale, action))
}

// toolPreviewTaskActionLabel localizes the task board's action names; an
// unknown action keeps its raw value.
func toolPreviewTaskActionLabel(locale contracts.LocaleService, action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "claim":
		return T(locale, "认领", "claim")
	case "complete":
		return T(locale, "完成", "complete")
	case "reopen":
		return T(locale, "重新打开", "reopen")
	case "release":
		return T(locale, "释放", "release")
	case "edit":
		return T(locale, "编辑", "edit")
	case "set_dependencies":
		return T(locale, "调整依赖", "set dependencies")
	case "reassign":
		return T(locale, "改派", "reassign")
	default:
		return strings.TrimSpace(action)
	}
}

// toolPreviewTeamTaskList describes the list query: the status and owner
// filters when present, and a plain list line otherwise.
func toolPreviewTeamTaskList(locale contracts.LocaleService, call contracts.ToolCall) string {
	var filters []string
	if status := strings.ToLower(strings.TrimSpace(toolPreviewArgValue(call.Arguments, "status"))); status != "" {
		filters = append(filters, toolPreviewTaskStatusLabel(locale, status))
	}
	if owner := toolPreviewArgValue(call.Arguments, "owner"); owner != "" {
		filters = append(filters, fmt.Sprintf(T(locale, "负责人：%s", "owner: %s"), owner))
	}
	if len(filters) == 0 {
		return T(locale, "列出团队任务", "List team tasks")
	}
	return fmt.Sprintf(T(locale, "列出团队任务（%s）", "List team tasks (%s)"),
		strings.Join(filters, T(locale, " · ", " · ")))
}

// toolPreviewTaskStatusLabel localizes a task board status; an unknown status
// keeps its raw value.
func toolPreviewTaskStatusLabel(locale contracts.LocaleService, status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return T(locale, "待处理", "pending")
	case "in_progress":
		return T(locale, "进行中", "in progress")
	case "completed":
		return T(locale, "已完成", "completed")
	default:
		return strings.TrimSpace(status)
	}
}

// toolPreviewArgValue reads the first present argument among keys as preview
// copy. It accepts a string value or the first string of an array (a
// web_search queries list), and reports "" for anything else so the caller
// falls back to the backend summary.
func toolPreviewArgValue(arguments string, keys ...string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return ""
	}
	for _, key := range keys {
		switch value := values[key].(type) {
		case string:
			if text := strings.TrimSpace(value); text != "" {
				return text
			}
		case []any:
			for _, entry := range value {
				if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
	}
	return ""
}

// ToolPanelSections builds the expanded panel of one tool call: the tool's
// input (bash command, write path+content, edit path+before/after, or pretty
// JSON), then its bounded output. Labels are localized through locale; colors
// are applied by the shared section renderer (ToolPanelSectionView) from the
// design tokens.
func ToolPanelSections(locale contracts.LocaleService, call contracts.ToolCall) []ToolPanelSection {
	tr := func(zh, en string) string { return T(locale, zh, en) }
	name := strings.ToLower(strings.TrimSpace(call.Name))
	arguments := strings.TrimSpace(call.Arguments)
	sections := make([]ToolPanelSection, 0, 4)

	switch {
	case name == "bash", name == "pwsh":
		if command := toolPreviewStringArg(arguments, "command"); command != "" {
			sections = append(sections, ToolPanelSection{
				Label:     tr("命令", "Command"),
				Text:      command,
				Mono:      true,
				Bounded:   true,
				CopyLabel: tr("复制命令", "Copy command"),
			})
		} else if generic, ok := toolPreviewGenericInput(tr, arguments); ok {
			sections = append(sections, generic)
		}
	case name == "write_file":
		if path := toolPreviewStringArg(arguments, "path"); path != "" {
			sections = append(sections, ToolPanelSection{
				Label:     tr("路径", "Path"),
				Text:      path,
				Mono:      true,
				CopyLabel: tr("复制路径", "Copy path"),
			})
		}
		if content := toolPreviewStringArg(arguments, "content"); content != "" {
			text, truncated := toolPreviewBoundRunes(content, toolPreviewMaxContentRunes)
			section := ToolPanelSection{
				Label:     tr("内容", "Content"),
				Text:      text,
				Mono:      true,
				Bounded:   true,
				CopyLabel: tr("复制内容", "Copy content"),
			}
			if truncated {
				section.Hint = tr("内容已截断，仅显示前部分", "Content truncated; only the beginning is shown")
			}
			sections = append(sections, section)
		}
	case name == "edit_file", name == "str_replace_editor":
		if path := toolPreviewStringArg(arguments, "path"); path != "" {
			sections = append(sections, ToolPanelSection{
				Label:     tr("路径", "Path"),
				Text:      path,
				Mono:      true,
				CopyLabel: tr("复制路径", "Copy path"),
			})
		}
		oldText := toolPreviewFirstStringArg(arguments, "old_string", "old_str")
		newText := toolPreviewFirstStringArg(arguments, "new_string", "new_str")
		if oldText != "" {
			sections = append(sections, ToolPanelSection{
				Label:     tr("替换前", "Before"),
				Text:      oldText,
				Mono:      true,
				Bounded:   true,
				Tone:      ToolSectionRemoved,
				CopyLabel: tr("复制替换前", "Copy before"),
			})
		}
		if newText != "" {
			sections = append(sections, ToolPanelSection{
				Label:     tr("替换后", "After"),
				Text:      newText,
				Mono:      true,
				Bounded:   true,
				Tone:      ToolSectionAdded,
				CopyLabel: tr("复制替换后", "Copy after"),
			})
		}
		// str_replace_editor's create command carries the new file body instead
		// of an old/new pair.
		if oldText == "" && newText == "" {
			if content := toolPreviewFirstStringArg(arguments, "file_text", "content"); content != "" {
				text, truncated := toolPreviewBoundRunes(content, toolPreviewMaxContentRunes)
				section := ToolPanelSection{
					Label:     tr("内容", "Content"),
					Text:      text,
					Mono:      true,
					Bounded:   true,
					CopyLabel: tr("复制内容", "Copy content"),
				}
				if truncated {
					section.Hint = tr("内容已截断，仅显示前部分", "Content truncated; only the beginning is shown")
				}
				sections = append(sections, section)
			}
		}
	default:
		if generic, ok := toolPreviewGenericInput(tr, arguments); ok {
			sections = append(sections, generic)
		}
	}

	if summary := strings.TrimSpace(call.Summary); summary != "" {
		section := ToolPanelSection{
			Label:     tr("输出", "Output"),
			Text:      summary,
			Mono:      true,
			Bounded:   true,
			CopyLabel: tr("复制输出", "Copy output"),
		}
		if toolPreviewOutputTruncated(summary) {
			section.Hint = tr("输出已截断，仅显示前部分", "Output truncated; only the beginning is shown")
		}
		sections = append(sections, section)
	}
	return sections
}

// toolPreviewGenericInput renders the tool arguments as indented JSON, bounded
// and copyable. It reports false when there is nothing readable to show.
func toolPreviewGenericInput(tr func(zh, en string) string, arguments string) (ToolPanelSection, bool) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return ToolPanelSection{}, false
	}
	text := trimmed
	if pretty, ok := toolPreviewPrettyJSON(trimmed); ok {
		text = pretty
	}
	bounded, truncated := toolPreviewBoundRunes(text, toolPreviewMaxInputRunes)
	section := ToolPanelSection{
		Label:     tr("输入", "Input"),
		Text:      bounded,
		Mono:      true,
		Bounded:   true,
		CopyLabel: tr("复制输入", "Copy input"),
	}
	if truncated {
		section.Hint = tr("输入已截断，仅显示前部分", "Input truncated; only the beginning is shown")
	}
	return section, true
}

// toolPreviewStringArg reads one string field of the tool's JSON arguments (""
// when the arguments are not JSON or the field is absent/non-string).
func toolPreviewStringArg(arguments, key string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

// toolPreviewFirstStringArg returns the first present string field among keys.
func toolPreviewFirstStringArg(arguments string, keys ...string) string {
	for _, key := range keys {
		if value := toolPreviewStringArg(arguments, key); value != "" {
			return value
		}
	}
	return ""
}

// toolPreviewPrettyJSON indents one JSON document for the input section.
func toolPreviewPrettyJSON(raw string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") && !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		return "", false
	}
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, []byte(raw), "", "  "); err != nil {
		return "", false
	}
	return buffer.String(), true
}

// toolPreviewBoundRunes cuts text to max runes (UTF-8 safe) and reports whether
// it cut.
func toolPreviewBoundRunes(text string, max int) (string, bool) {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text, false
	}
	return string(runes[:max]) + "…", true
}

// toolPreviewOutputTruncated reports whether the backend cut the read-model
// summary: the agent appends the truncation ellipsis when the output exceeds
// its bound.
func toolPreviewOutputTruncated(summary string) bool {
	return strings.HasSuffix(strings.TrimSpace(summary), "…") &&
		len([]rune(strings.TrimSpace(summary))) >= toolPreviewAssumedSummaryRunes
}
