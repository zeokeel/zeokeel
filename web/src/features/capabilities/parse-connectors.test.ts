import { describe, expect, it } from 'vitest';
import { parseConnectors } from './capabilities-screen';

/**
 * `mcp.servers` is also reachable as a raw settings row, so the page must tell
 * "nothing configured" apart from "hand-edited into something we cannot render".
 */
describe('parseConnectors', () => {
  it('is empty (not broken) for an absent row', () => {
    expect(parseConnectors('')).toEqual({ rows: [], broken: false });
  });

  it('keeps rows and reports a clean list', () => {
    const rows = [{ name: 'a', transport: 'http', url: 'http://x/mcp' }];
    expect(parseConnectors(JSON.stringify(rows))).toEqual({ rows, broken: false });
  });

  it('drops non-rows but remembers that it had to', () => {
    expect(parseConnectors('[null,1,"x"]')).toEqual({ rows: [], broken: true });
  });

  it('reports a non-array and invalid JSON as broken', () => {
    expect(parseConnectors('{"name":"a"}')).toEqual({ rows: [], broken: true });
    expect(parseConnectors('broken')).toEqual({ rows: [], broken: true });
  });

  it('keeps the good rows and flags the bad ones', () => {
    const parsed = parseConnectors('[{"name":"a"},null]');
    expect(parsed.rows).toEqual([{ name: 'a' }]);
    expect(parsed.broken).toBe(true);
  });
});
