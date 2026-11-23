import { describe, expect, it } from 'vitest';
import { GenericToolCard, RENDERER_NAMES, TOOL_RENDERERS, renderToolCard, toolRenderer } from './registry';
import { TOOL_META, TOOL_NAMES, toolMeta } from './meta';
import { languageForPath, valueText } from './sections';

describe('tool card dispatch', () => {
  it('has a renderer for every registered tool name', () => {
    // 26 real tools + the host's demo probe (internal/ui/toolcards/toolcards.go).
    expect(TOOL_NAMES).toHaveLength(27);
    for (const name of TOOL_NAMES) {
      expect(TOOL_RENDERERS[name], `missing renderer for ${name}`).toBeTypeOf('function');
      expect(toolMeta(name).label).not.toBe(name);
    }
  });

  it('keeps the 26 real tool names in sync with the metadata table', () => {
    const real = TOOL_NAMES.filter((name) => name !== 'demo');
    expect(real).toHaveLength(26);
    expect(RENDERER_NAMES).toHaveLength(27);
  });

  it('falls back to the generic card for an unregistered tool (MCP, future tools)', () => {
    expect(toolRenderer('mcp__filesystem__read')).toBe(GenericToolCard);
    const step = {
      callId: 'c1',
      name: 'mcp__filesystem__read',
      status: 'ok' as const,
      input: { path: '/a' },
      output: 'contents',
    };
    expect(renderToolCard(step)).toBeTruthy();
  });

  it('labels each category with a brand icon from internal/icons', () => {
    expect(toolMeta('bash').icon).toBe('terminal');
    expect(toolMeta('read_file').icon).toBe('file');
    expect(toolMeta('plan_review').icon).toBe('sparkles');
    // Unknown names keep their raw id and a neutral icon.
    expect(toolMeta('whatever')).toEqual({
      label: 'whatever',
      icon: 'plugin',
      category: 'other',
    });
    expect(TOOL_META.write_file?.category).toBe('file');
  });
});

describe('tool card helpers', () => {
  it('maps a file extension to a highlighter language', () => {
    expect(languageForPath('a/b.tsx')).toBe('tsx');
    expect(languageForPath('go.mod')).toBe('text');
    expect(languageForPath(undefined)).toBe('text');
  });

  it('renders any value as display text without throwing', () => {
    expect(valueText('x')).toBe('x');
    expect(valueText({ a: 1 })).toContain('"a": 1');
    const circular: Record<string, unknown> = {};
    circular.self = circular;
    expect(valueText(circular)).toBe('[object Object]');
  });
});

