package uikit

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ToolDisplayName renders a friendly, localized name for a tool call. The
// approval dialog shows it instead of the raw English tool ID; the raw ID stays
// available as the badge's tooltip. Unknown or empty names are returned
// unchanged, so a host-registered tool never disappears behind a generic label.
//
// Families whose members share a meaning fall back to the family name
// (terminal_*, job_*, memory_*, team_task_*), so a new member does not surface
// its raw identifier.
func ToolDisplayName(locale contracts.LocaleService, name string) string {
	zh, en := toolDisplayNamePair(strings.ToLower(strings.TrimSpace(name)))
	if zh == "" {
		return strings.TrimSpace(name)
	}
	if Language(locale) == "en" {
		return en
	}
	return zh
}

// toolDisplayNamePair resolves one tool name to its zh/en display pair ("" for
// a tool the kit does not know).
func toolDisplayNamePair(key string) (zh, en string) {
	switch {
	case key == "read_file":
		return "读取文件", "Read file"
	case key == "read_image":
		return "查看图片", "View image"
	case key == "write_file":
		return "写入文件", "Write file"
	case key == "edit_file", key == "str_replace_editor":
		return "编辑文件", "Edit file"
	case key == "list_dir":
		return "列出目录", "List directory"
	case key == "glob":
		return "按模式查找文件", "Find files by pattern"
	case key == "grep":
		return "搜索文件内容", "Search file contents"
	case key == "bash":
		return "运行命令", "Run command"
	case key == "pwsh":
		return "运行 PowerShell", "Run PowerShell"
	case key == "terminal_send", strings.HasPrefix(key, "terminal_"):
		return "发送终端命令", "Send terminal command"
	case key == "job_start", strings.HasPrefix(key, "job_"):
		return "后台任务", "Background job"
	case key == "web_fetch":
		return "抓取网页", "Fetch web page"
	case key == "web_search":
		return "搜索网页", "Search the web"
	case key == "working_directory":
		return "查看工作目录", "Show working directory"
	case key == "skill":
		return "加载技能", "Load skill"
	case strings.HasPrefix(key, "team_task_"):
		return "团队任务", "Team task"
	case strings.HasPrefix(key, "memory"):
		return "记忆", "Memory"
	case key == "todo":
		return "更新待办", "Update todo list"
	case key == "subagent":
		return "启动子代理", "Start subagent"
	case key == "workflow":
		return "运行工作流", "Run workflow"
	case key == "ralph":
		return "循环执行", "Run loop"
	case key == "plan_review":
		return "提交计划", "Submit plan"
	case key == "exit_plan_mode":
		return "退出计划模式", "Exit plan mode"
	case key == "ask_user":
		return "向用户提问", "Ask the user"
	case key == "enter_worktree":
		return "进入工作树", "Enter worktree"
	case key == "list_worktrees":
		return "列出工作树", "List worktrees"
	default:
		return "", ""
	}
}
