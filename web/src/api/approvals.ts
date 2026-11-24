/**
 * Approval helpers on top of the `approvals` domain (docs/AGENT-SURFACES.md S4).
 *
 * The queue itself lives in `gateway.ts` (WS `approvals.snapshot` /
 * `approvals.delta`, command `approval.resolve`). This module only holds local
 * presentation logic: expiry, the target block and the impact class.
 */
import type { ApprovalRequest } from './gateway';

export type { ApprovalRequest } from './gateway';

export type ApprovalDecision = 'allow' | 'allow_always' | 'deny';

/** True once the request's expiry has passed (a dead request is not actionable). */
export function isExpired(request: ApprovalRequest, now = Date.now()): boolean {
  if (!request.expiresAt) return false;
  const expiry = Date.parse(request.expiresAt);
  return Number.isFinite(expiry) && expiry <= now;
}

const FILE_TOOLS = new Set(['write_file', 'edit_file', 'str_replace_editor']);
const SHELL_TOOLS = new Set(['bash', 'terminal_send', 'job_start', 'terminal_read']);

export interface ApprovalTarget {
  kind: 'command' | 'path';
  text: string;
}

/** Parse the wire's raw-arguments JSON and pick the thing being acted on. */
export function approvalArguments(request: ApprovalRequest): Record<string, unknown> {
  if (!request.arguments) return {};
  try {
    const parsed = JSON.parse(request.arguments) as unknown;
    return parsed !== null && typeof parsed === 'object'
      ? (parsed as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}

export function approvalTarget(request: ApprovalRequest): ApprovalTarget | undefined {
  const args = approvalArguments(request);
  if (FILE_TOOLS.has(request.tool)) {
    const path = args.path ?? args.file_path ?? args.filename;
    return typeof path === 'string' ? { kind: 'path', text: path } : undefined;
  }
  const command = args.command ?? args.cmd ?? args.input ?? args.text;
  if (typeof command === 'string' && command.trim() !== '') {
    return { kind: 'command', text: command };
  }
  const path = args.path;
  if (typeof path === 'string') return { kind: 'path', text: path };
  return undefined;
}

export type ApprovalRisk = 'exec' | 'write' | 'network' | 'read';

/**
 * The tools that only look. Everything else is classified conservatively: the
 * card's sentence is a safety claim, and "this will read the workspace" for a
 * subagent (which runs arbitrary tools) or an MCP server is the one wrong direction
 * that matters. A live audit found exactly that — `subagent` produced the read
 * sentence because anything unrecognised fell through to 'read'.
 */
const READ_TOOLS = new Set([
  'read_file',
  'list_dir',
  'list_dir_inside',
  'glob',
  'grep',
  'grep_inside',
  'tool_search',
  'skill',
  'skills',
  'exit_plan_mode',
  'plan_review',
]);

export function approvalRisk(request: ApprovalRequest): ApprovalRisk {
  if (SHELL_TOOLS.has(request.tool)) return 'exec';
  if (FILE_TOOLS.has(request.tool)) return 'write';
  if (READ_TOOLS.has(request.tool)) return 'read';
  if (request.tool.startsWith('web_')) return 'network';
  // Shell, delegation (subagent/subagent_fork), MCP tools and anything the client
  // has never heard of: all of them can act, so none of them is a read.
  return 'exec';
}