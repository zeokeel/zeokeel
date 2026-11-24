import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ThoughtSteps } from './thought-steps';
import type { ToolStep } from './types';

/**
 * The one-line preview under a tool step. Two real reports from a run against a
 * live model (v0.3.2, qwen3.8-flash-next):
 *
 *   bash "{\"command\":\"echo zeokeel-tool-ok\",\"description\":\"Echo a test string\"}"
 *   memory "{\"action\":\"write\",\"content\":\"\u7528\u6237\u7684\u9879\u76ee\u4ee3\u53f7…\"}"
 *
 * The wire sends `input` as the arguments **string**, and the preview stringified it
 * again, so the reader got escaped quotes, escaped Chinese, and none of the target
 * (the command, the path, the query) the preview exists to show.
 */
const step = (input: unknown): ToolStep => ({
  callId: 'call-1',
  name: 'bash',
  input,
  status: 'ok',
});

function previewText(input: unknown): string {
  render(<ThoughtSteps reasoning="" steps={[step(input)]} streaming={false} />);
  return document.body.textContent ?? '';
}

describe('tool step preview', () => {
  it('shows the command for a JSON string of arguments, unescaped', () => {
    const text = previewText('{"command":"echo zeokeel-tool-ok","description":"Echo a test string"}');
    expect(text).toContain('echo zeokeel-tool-ok');
    // No double encoding: neither the escapes nor the outer braces-as-text.
    expect(text).not.toContain('\\"');
    expect(text).not.toContain('{"command"');
  });

  it('decodes escaped Chinese instead of showing \\uXXXX', () => {
    const text = previewText('{"action":"write","content":"\\u7528\\u6237\\u7684\\u9879\\u76ee"}');
    expect(text).toContain('用户的项目');
    expect(text).not.toContain('\\u7528');
  });

  it('still reads a parsed object', () => {
    const text = previewText({ command: 'go test ./calc/', description: 'run tests' });
    expect(text).toContain('go test ./calc/');
  });

  it('keeps a non-JSON string as the preview', () => {
    const text = previewText('just some text, not json');
    expect(text).toContain('just some text, not json');
  });

  it('never renders the raw call id as the preview', () => {
    const text = previewText('{"path":"calc/calc.go"}');
    expect(text).toContain('calc/calc.go');
    expect(text).not.toContain('call-1');
  });
});
// The collapsed "raw arguments" panel reads the same text; it must not show the
// escapes either, and a shell command must not be wrapped in quotes.
describe('valueText for tool payloads', () => {
  it('pretty-prints JSON text and decodes its escapes', async () => {
    const { valueText } = await import('./tools/sections');
    expect(valueText('{"content":"\\u7528\\u6237"}')).toContain('用户');
    expect(valueText('{"content":"\\u7528\\u6237"}')).not.toContain('\\u7528');
  });

  it('keeps a plain string verbatim', async () => {
    const { valueText } = await import('./tools/sections');
    expect(valueText('echo zeokeel-tool-ok')).toBe('echo zeokeel-tool-ok');
  });
});

describe('formatDuration', () => {
  it('reads like the native 用时 line', async () => {
    const { formatDuration } = await import('./thought-steps');
    expect(formatDuration(158)).toBe('158ms');
    expect(formatDuration(2300)).toBe('2.3s');
    expect(formatDuration(95_000)).toBe('1m35s');
  });
});
