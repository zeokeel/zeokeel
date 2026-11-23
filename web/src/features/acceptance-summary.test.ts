import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * The ACCEPTANCE summary line must equal its own table.
 *
 * It drifted once (the table said 22 rows all 完成, the summary still said
 * `16 / 4 / 2`) and nothing noticed, because a summary is prose that nobody
 * re-derives. A summary that can disagree with its table is a report that can
 * lie — so the table is now the input and the summary the assertion.
 */
const here = dirname(fileURLToPath(import.meta.url));
const file = readFileSync(resolve(here, '../../ACCEPTANCE-P1P2.md'), 'utf8');

const rows = file
  .split('\n')
  .filter((line) => /^\| (B|C)\d+\w* \|/.test(line))
  .map((line) => line.split('|').map((cell) => cell.trim()))
  .map((cells) => ({ id: cells[1], status: cells[3] }));

describe('ACCEPTANCE-P1P2.md', () => {
  it('has the 22 P1/P2 rows', () => {
    expect(rows.length).toBe(22);
    expect(new Set(rows.map((row) => row.id)).size).toBe(22);
  });

  it('uses one of the three statuses, spelled consistently', () => {
    const allowed = new Set(['完成', '部分', '未做']);
    const bad = rows.filter((row) => !allowed.has(row.status));
    expect(bad.map((row) => `${row.id}=${row.status}`)).toEqual([]);
  });

  it('keeps the summary line equal to the table', () => {
    const count = (status: string) => rows.filter((row) => row.status === status).length;
    const expected = `**完成 ${count('完成')} / 部分 ${count('部分')} / 未做 ${count('未做')}**（${rows.length} 条）`;
    const summary = file.split('\n').find((line) => line.includes('完成 ') && line.includes('未做')) ?? '';
    expect(summary.trim()).toBe(expected);
  });

  it('does not claim a reason for a row that is done', () => {
    const reasons = file
      .split('\n')
      .filter((line) => /^\| (B|C)\d+\w* \|/.test(line) && line.includes('未做'))
      .map((line) => line.split('|')[1].trim());
    expect(reasons).toEqual([]);
  });
});
