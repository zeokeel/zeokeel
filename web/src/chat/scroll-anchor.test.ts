import { describe, expect, it } from 'vitest';
import { anchorStorageKey, indexAtOffset, loadAnchor, resolveAnchor, saveAnchor } from './scroll-anchor';

function memoryStore() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => {
      map.set(key, value);
    },
    raw: map,
  };
}

describe('scroll anchor storage', () => {
  it('round-trips the anchor per session', () => {
    const store = memoryStore();
    saveAnchor(store, 's-1', { messageId: 'm-20', offset: 40, atBottom: false });
    saveAnchor(store, 's-2', { messageId: 'm-3', offset: 0, atBottom: true });

    expect(loadAnchor(store, 's-1')).toEqual({ messageId: 'm-20', offset: 40, atBottom: false });
    expect(loadAnchor(store, 's-2')).toEqual({ messageId: 'm-3', offset: 0, atBottom: true });
    expect(store.raw.has(anchorStorageKey('s-1'))).toBe(true);
  });

  it('ignores a corrupted entry instead of breaking the transcript', () => {
    const store = memoryStore();
    store.setItem(anchorStorageKey('s-1'), '{not json');
    expect(loadAnchor(store, 's-1')).toBeNull();
    store.setItem(anchorStorageKey('s-1'), JSON.stringify({ offset: 10 }));
    expect(loadAnchor(store, 's-1')).toBeNull();
    store.setItem(anchorStorageKey('s-1'), JSON.stringify({ messageId: 'm', offset: 'x' }));
    expect(loadAnchor(store, 's-1')).toBeNull();
  });

  it('clamps a negative offset', () => {
    const store = memoryStore();
    store.setItem(anchorStorageKey('s-1'), JSON.stringify({ messageId: 'm', offset: -20 }));
    expect(loadAnchor(store, 's-1')?.offset).toBe(0);
  });
});

describe('resolveAnchor', () => {
  const messages = [{ id: 'm-1' }, { id: 'm-2' }, { id: 'm-3' }];

  it('restores the anchored row and offset when detached', () => {
    expect(resolveAnchor({ messageId: 'm-2', offset: 30, atBottom: false }, messages)).toEqual({
      index: 1,
      offset: 30,
      atBottom: false,
    });
  });

  it('follows the newest message when the reader was at the bottom', () => {
    expect(resolveAnchor({ messageId: 'm-2', offset: 30, atBottom: true }, messages)).toEqual({
      index: -1,
      offset: 0,
      atBottom: true,
    });
  });

  it('falls back to following when the anchor message no longer exists', () => {
    expect(resolveAnchor({ messageId: 'gone', offset: 30, atBottom: false }, messages)).toEqual({
      index: -1,
      offset: 0,
      atBottom: true,
    });
    expect(resolveAnchor(null, messages)).toEqual({ index: -1, offset: 0, atBottom: true });
    expect(resolveAnchor(null, [])).toEqual({ index: -1, offset: 0, atBottom: true });
  });
});
describe('indexAtOffset', () => {
  it('uses the estimate grid before any measurement', () => {
    // 60 rows at 140px; a viewport top at 7040px sits on row 50, 40px in.
    const { index, start } = indexAtOffset({
      count: 60,
      scrollTop: 7040,
      estimate: 140,
      offsetOf: () => null,
      measured: false,
    });
    expect(index).toBe(50);
    expect(7040 - start).toBe(40);
  });

  it('uses measured offsets when the layout is real', () => {
    // The same rows, but measured at 200px each.
    const offsetOf = (index: number) => index * 200;
    expect(
      indexAtOffset({ count: 60, scrollTop: 8040, estimate: 140, offsetOf, measured: true }),
    ).toEqual({ index: 40, start: 8000 });
    // A scroll exactly on a row boundary belongs to that row.
    expect(
      indexAtOffset({ count: 60, scrollTop: 8000, estimate: 140, offsetOf, measured: true }).index,
    ).toBe(40);
  });

  it('never reports a negative or out-of-range row', () => {
    expect(
      indexAtOffset({ count: 3, scrollTop: -50, estimate: 140, offsetOf: () => null, measured: false })
        .index,
    ).toBe(0);
    expect(
      indexAtOffset({
        count: 3,
        scrollTop: 99_000,
        estimate: 140,
        offsetOf: () => null,
        measured: false,
      }).index,
    ).toBe(2);
    expect(
      indexAtOffset({ count: 0, scrollTop: 100, estimate: 140, offsetOf: () => null, measured: false }),
    ).toEqual({ index: 0, start: 0 });
  });
});
