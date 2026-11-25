import { describe, expect, it } from 'vitest';
import type { InboxItem } from '@/api/inbox';
import { translate } from '@/i18n';
import { formatRelativeTime, groupInboxItems, isSettled, type Translate } from './state';

/** The real catalogue, so an assertion here is the copy the screen shows. */
const zh: Translate = (key, params) => translate('zh', key, params);
const en: Translate = (key, params) => translate('en', key, params);

function item(id: string, status: string, extra: Partial<InboxItem> = {}): InboxItem {
  return { id, source: 'wechat', title: `title ${id}`, status, count: 1, ...extra };
}

describe('groupInboxItems', () => {
  it('puts unsettled rows in "now" and decided rows in "triaged"', () => {
    const [now, triaged] = groupInboxItems([
      item('a', 'new'),
      item('b', 'task'),
      item('c', 'ignored'),
      item('d', 'archived'),
      item('e', 'triaged'),
    ]);

    expect(now.key).toBe('now');
    expect(now.items.map((row) => row.id)).toEqual(['a']);
    expect(triaged.key).toBe('triaged');
    expect(triaged.items.map((row) => row.id)).toEqual(['b', 'c', 'd', 'e']);
  });

  it('always returns both sections, in that order, so the caller can hide one', () => {
    const sections = groupInboxItems([]);
    expect(sections.map((section) => section.key)).toEqual(['now', 'triaged']);
    expect(sections.every((section) => section.items.length === 0)).toBe(true);
  });

  it('keeps the gateway order inside a section — no local sort', () => {
    const sections = groupInboxItems([item('z', 'new'), item('a', 'new'), item('m', 'new')]);
    expect(sections[0].items.map((row) => row.id)).toEqual(['z', 'a', 'm']);
  });

  it('keeps a row whose status the client does not know yet', () => {
    // An unknown status is not proof that the Lead decided anything: hiding the
    // row would silently drop work, so it stays in "now".
    expect(isSettled('something-new')).toBe(false);
    expect(groupInboxItems([item('x', 'something-new')])[0].items.map((row) => row.id)).toEqual([
      'x',
    ]);
  });
});

describe('formatRelativeTime', () => {
  const now = Date.parse('2026-11-25T12:00:00Z');

  it('reads "刚刚" within the first minute, including a slightly future stamp', () => {
    expect(formatRelativeTime('2026-11-25T12:00:00Z', now, zh)).toBe('刚刚');
    expect(formatRelativeTime('2026-11-25T11:59:30Z', now, zh)).toBe('刚刚');
    // Gateway clock ahead of the browser: not an error the reader can act on.
    expect(formatRelativeTime('2026-11-25T12:00:30Z', now, zh)).toBe('刚刚');
  });

  it('counts minutes, hours and days', () => {
    expect(formatRelativeTime('2026-11-25T11:58:00Z', now, zh)).toBe('2 分钟前');
    expect(formatRelativeTime('2026-11-25T11:59:00Z', now, zh)).toBe('1 分钟前');
    expect(formatRelativeTime('2026-11-25T09:00:00Z', now, zh)).toBe('3 小时前');
    expect(formatRelativeTime('2026-11-23T12:00:00Z', now, zh)).toBe('2 天前');
  });

  it('falls back to a calendar date past a month', () => {
    const then = new Date(2026, 0, 5, 12, 0, 0);
    const label = formatRelativeTime(then.toISOString(), Date.parse('2026-11-25T12:00:00Z'), zh);
    expect(label).toBe('2026-01-05');
  });

  it('takes its words from the catalogue, not from this module', () => {
    // The same instant, two languages: proves the stamp is assembled from keys.
    expect(formatRelativeTime('2026-11-25T11:58:00Z', now, en)).toBe('2 min ago');
    expect(formatRelativeTime('2026-11-25T12:00:00Z', now, en)).toBe('just now');
  });

  it('returns an empty label for a missing or unparseable stamp', () => {
    expect(formatRelativeTime(undefined, now, zh)).toBe('');
    expect(formatRelativeTime('not a date', now, zh)).toBe('');
  });
});