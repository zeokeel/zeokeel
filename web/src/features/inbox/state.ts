/**
 * Pure shaping for the inbound work inbox (`inbox.*`, docs/TRANSPORT.md §5.1.13).
 *
 * Kept out of the screen so the grouping and the clock rules can be tested
 * without a gateway, a DOM or a fake timer. Copy is not baked in here: the
 * relative stamp is assembled from catalogue keys through the `t` the caller
 * passes (`src/i18n`), so this file stays free of user-visible strings.
 */
import type { MessageKey } from '@/i18n';
import type { InboxItem } from '@/api/inbox';

/** The catalog translator (`useT()`, or `translate(locale, …)` in tests). */
export type Translate = (key: MessageKey, params?: Record<string, string | number>) => string;

/** The two sections the screen has. Keys, not copy: the screen owns the words. */
export type InboxSectionKey = 'now' | 'triaged';

export interface InboxSection {
  key: InboxSectionKey;
  /** Rows in the order the gateway returned them — never re-sorted here. */
  items: InboxItem[];
}

/**
 * Statuses that mean "the Lead already decided". `triaged` is in the set even
 * though the screen's second section is documented as task/ignored/archived:
 * the Lead marking an item seen is a decision too, and hiding it would make the
 * row vanish with no section to explain where it went.
 */
export const SETTLED_STATUSES: readonly string[] = ['task', 'ignored', 'archived', 'triaged'];

/**
 * True once the item is no longer in the "do it now" pile.
 *
 * An **unknown** status is treated as unsettled (`false`): a row the client has
 * not learned about yet must still be visible, and the inbox default list is
 * exactly "still actionable". Hiding it would silently drop work.
 */
export function isSettled(status: string): boolean {
  return SETTLED_STATUSES.includes(status);
}

/**
 * Split the queue into "现在必须做" (unsettled) and "已分诊" (settled).
 *
 * Both sections are always returned, in that order, so the caller decides
 * whether an empty half is worth rendering (a `0` count is not — WEB-CLIENT
 * A13). Order inside each section is the gateway's.
 */
export function groupInboxItems(items: readonly InboxItem[]): InboxSection[] {
  const now: InboxItem[] = [];
  const triaged: InboxItem[] = [];
  for (const item of items) {
    (isSettled(item.status) ? triaged : now).push(item);
  }
  return [
    { key: 'now', items: now },
    { key: 'triaged', items: triaged },
  ];
}

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;
/** Past this age a "N 天前" stops being readable and the date is honest. */
const ABSOLUTE_AFTER_MS = 30 * DAY_MS;

function two(value: number): string {
  return value < 10 ? `0${value}` : String(value);
}

/** Local calendar date (`YYYY-MM-DD`) — language-neutral, so no catalog key. */
function calendarDate(epochMs: number): string {
  const date = new Date(epochMs);
  return `${date.getFullYear()}-${two(date.getMonth() + 1)}-${two(date.getDate())}`;
}

/**
 * A relative stamp for a received timestamp — "2 分钟前" in Chinese, "2 min ago"
 * in English, taken from the catalogues through `t`.
 *
 * `now` is injected so the caller (and the tests) fix the clock instead of the
 * function reading it twice. Unparseable or missing input returns `''` — a
 * stamped row with a made-up time is worse than a row with no time.
 *
 * A future timestamp (clock skew between the gateway and the browser) reads as
 * "just now": it is not an error the reader can act on, and negative minutes
 * would be nonsense.
 */
export function formatRelativeTime(
  iso: string | undefined,
  now: number = Date.now(),
  t: Translate,
): string {
  if (!iso) return '';
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return '';

  const delta = now - then;
  if (delta < MINUTE_MS) return t('inbox.time.justNow');
  if (delta < HOUR_MS) return t('inbox.time.minutesAgo', { count: Math.floor(delta / MINUTE_MS) });
  if (delta < DAY_MS) return t('inbox.time.hoursAgo', { count: Math.floor(delta / HOUR_MS) });
  if (delta < ABSOLUTE_AFTER_MS) return t('inbox.time.daysAgo', { count: Math.floor(delta / DAY_MS) });
  return calendarDate(then);
}