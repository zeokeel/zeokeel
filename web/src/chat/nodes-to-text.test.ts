import { createElement } from 'react';
import { describe, expect, it } from 'vitest';
import { nodesToText } from './nodes-to-text';

describe('nodesToText', () => {
  it('flattens the children XMarkdown hands to a code block', () => {
    expect(nodesToText(['const a = 1;\n', 'const b = 2;\n'])).toBe('const a = 1;\nconst b = 2;\n');
  });

  it('walks element children', () => {
    const tree = createElement('span', null, 'line1', createElement('em', null, 'line2'));
    expect(nodesToText(tree)).toBe('line1line2');
  });

  it('returns empty for the empty cases', () => {
    expect(nodesToText(null)).toBe('');
    expect(nodesToText(undefined)).toBe('');
    expect(nodesToText(false)).toBe('');
    expect(nodesToText(42)).toBe('42');
  });
});