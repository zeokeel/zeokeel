import { describe, expect, it } from 'vitest';
import { approvalRisk } from './approvals';
import type { ApprovalRequest } from './approvals';

/**
 * The approval card's impact sentence is a safety claim, and a wrong one is worse
 * than a vague one. A live audit hit it: delegating to a subagent (which runs
 * arbitrary tools) produced the card "用户这会读取工作区里的内容" — "this will read
 * the workspace" — because the classifier returned 'read' for every name it did not
 * recognise, and `subagent`, `mcp__*` and `memory` are all unrecognised.
 */
const request = (tool: string): ApprovalRequest => ({ tool } as ApprovalRequest);

describe('approvalRisk', () => {
  it('calls a shell command an exec', () => {
    expect(approvalRisk(request('bash'))).toBe('exec');
    expect(approvalRisk(request('terminal_send'))).toBe('exec');
  });

  it('calls a file tool a write', () => {
    expect(approvalRisk(request('write_file'))).toBe('write');
    expect(approvalRisk(request('str_replace_editor'))).toBe('write');
  });

  it('calls a web tool a network call', () => {
    expect(approvalRisk(request('web_fetch'))).toBe('network');
  });

  it('still calls a read-only tool a read', () => {
    for (const tool of ['read_file', 'list_dir', 'glob', 'grep', 'tool_search']) {
      expect(approvalRisk(request(tool))).toBe('read');
    }
  });

  it('a delegation is not a read: it runs tools of its own', () => {
    // The live card said "this will read the workspace" for exactly these two.
    expect(approvalRisk(request('subagent'))).toBe('exec');
    expect(approvalRisk(request('subagent_fork'))).toBe('exec');
  });

  it('an MCP tool is not a read: it can do anything the server implements', () => {
    expect(approvalRisk(request('mcp__zk-test-mcp__mcp_echo_marker'))).toBe('exec');
  });

  it('memory is not a read: it is how facts get written', () => {
    expect(approvalRisk(request('memory'))).toBe('exec');
  });

  it('an unknown tool is never called a read', () => {
    // The safe default: claiming "read" for something we do not know is the one
    // direction that can mislead a user into approving more than they think.
    expect(approvalRisk(request('some_future_tool'))).not.toBe('read');
  });
});