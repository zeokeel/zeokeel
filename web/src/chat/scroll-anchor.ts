/**
 * Per-session scroll restoration (A3).
 *
 * Never a pixel offset: message heights change (a tool card opens, a code block
 * grows, images arrive), so a saved `scrollTop` lands somewhere else on return.
 * The anchor is (message id, offset inside that row) plus the one boolean the
 * user actually notices — were they following the bottom or not.
 */

export interface ScrollAnchor {
  /** The message id of the first visible row when the reader left. */
  messageId: string;
  /** Pixels scrolled past that row's top. */
  offset: number;
  /** True when the reader was within the follow threshold of the bottom. */
  atBottom: boolean;
}

export type AnchorStore = Pick<Storage, 'getItem' | 'setItem'>;

export const anchorStorageKey = (sessionId: string) => `zk.scroll.${sessionId}`;

export function loadAnchor(store: AnchorStore, sessionId: string): ScrollAnchor | null {
  try {
    const raw = store.getItem(anchorStorageKey(sessionId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ScrollAnchor>;
    if (typeof parsed.messageId !== 'string' || parsed.messageId === '') return null;
    if (typeof parsed.offset !== 'number' || !Number.isFinite(parsed.offset)) return null;
    return { messageId: parsed.messageId, offset: Math.max(0, parsed.offset), atBottom: !!parsed.atBottom };
  } catch {
    // A corrupted entry must never break the transcript: fall back to bottom.
    return null;
  }
}

export function saveAnchor(store: AnchorStore, sessionId: string, anchor: ScrollAnchor): void {
  try {
    store.setItem(anchorStorageKey(sessionId), JSON.stringify(anchor));
  } catch {
    // Quota or a disabled storage: scrolling still works, only restoration is lost.
  }
}

export interface ResolvedAnchor {
  /** Row to restore to, or -1 when the anchor cannot be honoured. */
  index: number;
  offset: number;
  atBottom: boolean;
}

/**
 * Where to land on return. A missing anchor, an unknown message id or a reader
 * who was following the bottom all resolve to "follow the newest message" —
 * never to the top.
 */
export function resolveAnchor(
  anchor: ScrollAnchor | null,
  messages: Array<{ id: string | number }>,
): ResolvedAnchor {
  if (!anchor || anchor.atBottom || messages.length === 0) {
    return { index: -1, offset: 0, atBottom: true };
  }
  const index = messages.findIndex((message) => String(message.id) === anchor.messageId);
  if (index < 0) return { index: -1, offset: 0, atBottom: true };
  return { index, offset: anchor.offset, atBottom: false };
}
export interface OffsetIndex {
  index: number;
  /** Top of that row in the same coordinate system used for `scrollTop`. */
  start: number;
}

/**
 * Which row owns a scroll offset, and where that row starts.
 *
 * `offsetOf` is the virtualizer's own answer (`getOffsetForIndex`). Before the
 * first measurement (and in a layout-less environment such as jsdom) it reports
 * nothing usable; then the estimate grid takes over, so capture and restore
 * still agree with each other instead of rounding to the top.
 */
export function indexAtOffset({
  count,
  scrollTop,
  estimate,
  offsetOf,
  measured,
}: {
  count: number;
  scrollTop: number;
  estimate: number;
  /** Null when the caller knows the virtualizer has not measured a layout. */
  offsetOf: (index: number) => number | null;
  measured: boolean;
}): OffsetIndex {
  if (count <= 0) return { index: 0, start: 0 };

  if (!measured) {
    const index = Math.min(count - 1, Math.max(0, Math.floor(scrollTop / estimate)));
    return { index, start: index * estimate };
  }

  let lo = 0;
  let hi = count - 1;
  let index = 0;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const start = offsetOf(mid);
    if (start !== null && start <= scrollTop) {
      index = mid;
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  return { index, start: offsetOf(index) ?? index * estimate };
}
