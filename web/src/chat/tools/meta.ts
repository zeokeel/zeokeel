import type { IconName } from '@/design/icons';

/**
 * Tool metadata: the 26 tool names that ship a dedicated card (mirrors
 * internal/ui/toolcards/toolcards.go `realToolNames`), plus the `demo` probe.
 *
 * Matching is by exact protocol name — no wildcards — so an MCP tool
 * (`mcp__<server>__<tool>`) or a future tool always lands on the generic card
 * instead of silently rendering nothing (toolcards.go:19-22 does the same).
 */

export type ToolCategory = 'shell' | 'file' | 'web' | 'job' | 'team' | 'plan' | 'skill' | 'other';

export interface ToolMeta {
  /** Friendly name for the card header; the protocol id stays in the title. */
  label: string;
  icon: IconName;
  category: ToolCategory;
}

export const TOOL_META: Record<string, ToolMeta> = {
  // shell / files
  bash: { label: '执行命令', icon: 'terminal', category: 'shell' },
  read_file: { label: '读取文件', icon: 'file', category: 'file' },
  write_file: { label: '写入文件', icon: 'file', category: 'file' },
  edit_file: { label: '编辑文件', icon: 'pencil', category: 'file' },
  str_replace_editor: { label: '编辑器替换', icon: 'pencil', category: 'file' },
  list_dir: { label: '列出目录', icon: 'folder', category: 'file' },
  glob: { label: '匹配文件', icon: 'search', category: 'file' },
  grep: { label: '搜索内容', icon: 'search', category: 'file' },
  read_image: { label: '查看图片', icon: 'image', category: 'file' },
  // web
  web_fetch: { label: '抓取网页', icon: 'globe', category: 'web' },
  web_search: { label: '搜索网页', icon: 'search', category: 'web' },
  // jobs / terminals
  job_start: { label: '启动后台任务', icon: 'activity', category: 'job' },
  job_output: { label: '读取任务输出', icon: 'activity', category: 'job' },
  terminal_send: { label: '向终端输入', icon: 'terminal', category: 'job' },
  terminal_read: { label: '读取终端', icon: 'terminal', category: 'job' },
  // worktrees
  enter_worktree: { label: '进入工作树', icon: 'data', category: 'team' },
  list_worktrees: { label: '列出工作树', icon: 'folder-open', category: 'team' },
  // orchestration
  subagent: { label: '子代理', icon: 'users', category: 'team' },
  workflow: { label: '工作流', icon: 'activity', category: 'team' },
  ralph: { label: 'Ralph 迭代', icon: 'reload', category: 'team' },
  // team tasks / planning
  team_task_create: { label: '新建团队任务', icon: 'plus', category: 'team' },
  team_task_update: { label: '更新团队任务', icon: 'pencil', category: 'team' },
  todo: { label: '待办', icon: 'check', category: 'plan' },
  todo_write: { label: '写入待办', icon: 'check', category: 'plan' },
  plan_review: { label: '计划审阅', icon: 'sparkles', category: 'plan' },
  // skills
  skill: { label: '技能', icon: 'plugin', category: 'skill' },
  // host capability probe
  demo: { label: '示例卡片', icon: 'sparkles', category: 'other' },
};

export const TOOL_NAMES = Object.keys(TOOL_META);

/** Fallback meta for an unregistered tool: the raw name stays visible. */
export function toolMeta(name: string): ToolMeta {
  return TOOL_META[name] ?? { label: name, icon: 'plugin', category: 'other' };
}

/** Tools that change files: the approval card labels these by path, not command. */
export const FILE_WRITE_TOOLS = new Set(['write_file', 'edit_file', 'str_replace_editor']);

export const SHELL_TOOLS = new Set(['bash', 'terminal_send', 'job_start']);