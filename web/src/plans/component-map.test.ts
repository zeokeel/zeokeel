import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * `docs/plans/antd-x-component-map.md` describes the same facts twice: a mapping
 * table ("which component for what") and a "deliberately not used" section.
 * They drifted immediately — the table still said `Suggestion` powers the slash
 * menu after §7 said it must not — and the document briefly carried **two `## 6.`**
 * sections. Reading the table alone gives the wrong answer, so the two are
 * checked against each other here.
 */
const here = dirname(fileURLToPath(import.meta.url));
const doc = readFileSync(resolve(here, '../../../docs/plans/antd-x-component-map.md'), 'utf8');

const lines = doc.split('\n');

const sections = lines
  .map((line, index) => ({ line, index }))
  .filter((entry) => /^## \d+\./.test(entry.line))
  .map((entry) => ({
    number: Number(/^## (\d+)\./.exec(entry.line)?.[1] ?? -1),
    title: entry.line.replace(/^## \d+\.\s*/, ''),
    index: entry.index,
  }));

describe('component map document', () => {
  it('numbers its sections once, in order', () => {
    const numbers = sections.map((section) => section.number);
    expect(numbers, '章节编号重复').toEqual([...new Set(numbers)]);
    expect(numbers, '章节编号不递增').toEqual([...numbers].sort((a, b) => a - b));
  });

  it('has a "deliberately not used" section', () => {
    expect(sections.some((item) => item.title.includes('有意不用'))).toBe(true);
  });

  it('never lists a deliberately-unused component as in use', () => {
    const notUsed = sections.find((item) => item.title.includes('有意不用'));
    expect(notUsed, '找不到「有意不用」章节').toBeTruthy();
    const nextSection = sections.find((item) => item.number > notUsed!.number);
    const body = lines.slice(notUsed!.index, nextSection?.index ?? lines.length).join('\n');
    const declared = [...body.matchAll(/^### \d+\.\d+ `([A-Za-z]+)`/gm)].map((match) => match[1]);
    expect(declared.length, '「有意不用」里没有声明任何组件').toBeGreaterThan(0);

    const offenders = lines
      .filter((line) => line.startsWith('|'))
      .filter((line) => declared.some((name) => line.includes(`\`${name}\``)))
      .filter((line) => !/见 §7/.test(line));
    expect(offenders, '这些表格行把「有意不用」的组件写成了在用').toEqual([]);
  });
});
