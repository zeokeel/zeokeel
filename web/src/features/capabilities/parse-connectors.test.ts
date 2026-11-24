import { describe, expect, it } from 'vitest';
import { parseConnectors } from './capabilities-screen';

describe('parseConnectors', () => {
  it('is empty for an absent row', () => {
    expect(parseConnectors('')).toEqual([]);
  });

  it('keeps rows with a name', () => {
    const rows = [{ name: 'a', transport: 'http', url: 'http://x/mcp' }];
    expect(parseConnectors(JSON.stringify(rows))).toEqual(rows);
  });

  it('drops non-objects and non-arrays instead of throwing', () => {
    expect(parseConnectors('[null,1,"x"]')).toEqual([]);
    expect(parseConnectors('{"name":"a"}')).toEqual([]);
    expect(parseConnectors('broken')).toEqual([]);
  });
});
