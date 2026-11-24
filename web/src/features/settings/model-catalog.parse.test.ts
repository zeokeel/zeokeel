import { describe, expect, it } from 'vitest';
import { parseRows } from './model-catalog';

/**
 * The catalogue's parse step is where "valid JSON" and "a list of models" stop
 * being the same thing. `[null]` used to pass the array check and then crash the
 * whole settings tree on `row.id` (round-3 model audit P0-B).
 */
describe('parseRows', () => {
  it('treats an absent row as "not configured", not as broken', () => {
    expect(parseRows('')).toEqual({ rows: [], broken: false });
    expect(parseRows('   ')).toEqual({ rows: [], broken: false });
  });

  it('treats a deliberately emptied list as empty, not broken', () => {
    expect(parseRows('[]')).toEqual({ rows: [], broken: false });
  });

  it('keeps well-formed rows untouched', () => {
    const rows = [{ id: 'a', name: 'A', contextWindow: 8192 }];
    expect(parseRows(JSON.stringify(rows))).toEqual({ rows, broken: false });
  });

  it.each([
    ['null', '[null]'],
    ['an empty object', '[{}]'],
    ['numbers', '[1,2,3]'],
    ['a blank id', '[{"id":"   "}]'],
    ['a non-string id', '[{"id":1}]'],
    ['a mix', '[{"id":"ok"},null]'],
    ['a non-array', '{"id":"a"}'],
    ['invalid JSON', 'not json at all'],
  ])('reports %s as broken instead of rendering it', (_label, raw) => {
    expect(parseRows(raw)).toEqual({ rows: [], broken: true });
  });
});
