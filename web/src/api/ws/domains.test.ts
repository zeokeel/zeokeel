import { describe, expect, it, vi } from 'vitest';
import {
  SyncedDomain,
  applyItemSet,
  applyListDelta,
  applySettingsDelta,
  type DomainDelta,
} from './domains';

function domain<T>(initial: T, reduce: (value: T, delta: DomainDelta) => T) {
  let snapshot: T = initial;
  const loads: number[] = [];
  const instance = new SyncedDomain<T>(
    'test',
    async () => {
      loads.push(Date.now());
      return { value: snapshot, rev: 7, epoch: 'epoch-1' };
    },
    reduce,
  );
  return {
    instance,
    loads,
    setSnapshot: (next: T) => {
      snapshot = next;
    },
  };
}

describe('SyncedDomain', () => {
  it('applies a delta whose baseRev matches the held revision', async () => {
    const { instance } = domain<string[]>([], (list: string[], delta) => [
      ...list,
      ...((delta.added as string[]) ?? []),
    ]);
    await instance.reload();
    expect(instance.rev()).toBe(7);

    expect(instance.applyDelta({ baseRev: 7, rev: 8, added: ['a'] })).toBe('applied');
    expect(instance.value()).toEqual(['a']);
    expect(instance.rev()).toBe(8);
  });

  it('re-snapshots when the base revision does not match (a missed delta)', async () => {
    const { instance, loads } = domain<string[]>([], (list) => list);
    await instance.reload();
    const before = loads.length;

    expect(instance.applyDelta({ baseRev: 3, rev: 4, added: ['a'] })).toBe('resync');
    await Promise.resolve();
    await Promise.resolve();
    expect(loads.length).toBe(before + 1);
  });

  it('re-snapshots when the gateway marks the delta as resync', async () => {
    const { instance, loads } = domain<string[]>([], (list) => list);
    await instance.reload();
    const before = loads.length;

    expect(instance.applyDelta({ baseRev: 7, rev: 9, resync: true })).toBe('resync');
    await Promise.resolve();
    await Promise.resolve();
    expect(loads.length).toBe(before + 1);
  });

  it('re-snapshots when no snapshot was held yet', () => {
    const { instance } = domain<string[]>([], (list) => list);
    expect(instance.applyDelta({ baseRev: 1, rev: 2 })).toBe('resync');
  });

  it('notifies subscribers with the current value on subscribe and after deltas', async () => {
    const { instance } = domain<number[]>([], (list, delta) => [
      ...list,
      Number((delta.added as number[] | undefined)?.[0] ?? 0),
    ]);
    await instance.reload();
    const seen: number[][] = [];
    const unsubscribe = instance.subscribe((value) => seen.push(value));

    instance.applyDelta({ baseRev: 7, rev: 8, added: [1] });
    expect(seen).toHaveLength(2);
    expect(seen[1]).toEqual([1]);

    unsubscribe();
    instance.applyDelta({ baseRev: 8, rev: 9, added: [2] });
    expect(seen).toHaveLength(2);
  });

  it('shares one request between concurrent reloads', async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const loader = vi.fn(async () => {
      await gate;
      return { value: 'v', rev: 1, epoch: 'e' };
    });
    const instance = new SyncedDomain<string>('test', loader, (value) => value);

    const first = instance.reload();
    const second = instance.reload();
    release();
    await Promise.all([first, second]);
    expect(loader).toHaveBeenCalledTimes(1);
  });
});

describe('reducers', () => {
  it('applies an item-set delta: removals first, then additions in order', () => {
    const items = [
      { id: 'a', text: 'A' },
      { id: 'b', text: 'B' },
    ];
    expect(
      applyItemSet(items, { baseRev: 1, rev: 2, added: [{ id: 'c', text: 'C' }], removed: ['a'] }),
    ).toEqual([
      { id: 'c', text: 'C' },
      { id: 'b', text: 'B' },
    ]);
  });

  it('applies a list delta with added, changed and removed', () => {
    const items = [
      { id: 'a', title: 'A' },
      { id: 'b', title: 'B' },
    ];
    const next = applyListDelta(items, {
      baseRev: 1,
      rev: 2,
      added: [{ id: 'c', title: 'C' }],
      changed: [{ id: 'b', title: 'B2' }],
      removed: ['a'],
    });
    expect(next).toEqual([
      { id: 'c', title: 'C' },
      { id: 'b', title: 'B2' },
    ]);
  });

  it('applies a settings delta and drops removed keys', () => {
    const rows = { 'a.b': '1', 'c.d': '2' };
    expect(
      applySettingsDelta(rows, { baseRev: 1, rev: 2, rows: { 'a.b': '9' }, removed: ['c.d'] }),
    ).toEqual({ 'a.b': '9' });
  });
});