import type { IconName } from '@/design/icons';
import type { MessageKey } from '@/i18n';

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
  /**
   * Copy key for the friendly name; the protocol id stays in the title. A key,
   * not a string: tool names are protocol values and their *labels* are copy.
   */
  labelKey: MessageKey;
  icon: IconName;
  category: ToolCategory;
}

export const TOOL_META: Record<string, ToolMeta> = {
  // shell / files
  bash: { labelKey: 'tool.bash', icon: 'terminal', category: 'shell' },
  read_file: { labelKey: 'tool.readFile', icon: 'file', category: 'file' },
  write_file: { labelKey: 'tool.writeFile', icon: 'file', category: 'file' },
  edit_file: { labelKey: 'tool.editFile', icon: 'pencil', category: 'file' },
  str_replace_editor: { labelKey: 'tool.replaceEditor', icon: 'pencil', category: 'file' },
  list_dir: { labelKey: 'tool.listDir', icon: 'folder', category: 'file' },
  glob: { labelKey: 'tool.glob', icon: 'search', category: 'file' },
  grep: { labelKey: 'tool.grep', icon: 'search', category: 'file' },
  read_image: { labelKey: 'tool.other', icon: 'image', category: 'file' },
  // web
  web_fetch: { labelKey: 'tool.webFetch', icon: 'globe', category: 'web' },
  web_search: { labelKey: 'tool.webSearch', icon: 'search', category: 'web' },
  // jobs / terminals
  job_start: { labelKey: 'tool.other', icon: 'activity', category: 'job' },
  job_output: { labelKey: 'tool.other', icon: 'activity', category: 'job' },
  terminal_send: { labelKey: 'tool.other', icon: 'terminal', category: 'job' },
  terminal_read: { labelKey: 'tool.other', icon: 'terminal', category: 'job' },
  // worktrees
  enter_worktree: { labelKey: 'tool.other', icon: 'data', category: 'team' },
  list_worktrees: { labelKey: 'tool.other', icon: 'folder-open', category: 'team' },
  // orchestration
  subagent: { labelKey: 'tool.other', icon: 'users', category: 'team' },
  workflow: { labelKey: 'tool.other', icon: 'activity', category: 'team' },
  ralph: { labelKey: 'tool.other', icon: 'reload', category: 'team' },
  // team tasks / planning
  team_task_create: { labelKey: 'tool.other', icon: 'plus', category: 'team' },
  team_task_update: { labelKey: 'tool.other', icon: 'pencil', category: 'team' },
  todo: { labelKey: 'tool.other', icon: 'check', category: 'plan' },
  todo_write: { labelKey: 'tool.todoWrite', icon: 'check', category: 'plan' },
  plan_review: { labelKey: 'tool.other', icon: 'sparkles', category: 'plan' },
  // skills
  skill: { labelKey: 'tool.other', icon: 'plugin', category: 'skill' },
  // host capability probe
  demo: { labelKey: 'tool.other', icon: 'sparkles', category: 'other' },
};

export const TOOL_NAMES = Object.keys(TOOL_META);

/** Fallback meta for an unregistered tool: the raw name stays visible. */
export function toolMeta(name: string): ToolMeta {
  return TOOL_META[name] ?? { label: name, icon: 'plugin', category: 'other' };
}

/** Tools that change files: the approval card labels these by path, not command. */
export const FILE_WRITE_TOOLS = new Set(['write_file', 'edit_file', 'str_replace_editor']);

export const SHELL_TOOLS = new Set(['bash', 'terminal_send', 'job_start']);