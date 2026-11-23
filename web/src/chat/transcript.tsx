import { useEffect, useRef, useState } from 'react';
import { Button } from 'antd';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { MessageInfo } from '@ant-design/x-sdk';
import { Bubble } from '@ant-design/x';
import { AssistantBody } from './assistant-body';
import {
  indexAtOffset,
  loadAnchor,
  resolveAnchor,
  saveAnchor,
  type AnchorStore,
} from './scroll-anchor';
import type { ZeoMessage } from './types';

const FALLBACK_ROWS = 40;
const SAVE_THROTTLE_MS = 150;
const ESTIMATED_ROW_HEIGHT = 140;

/** Are we close enough to the bottom to keep following the stream? (§7.2: 24px) */
export function shouldFollow(distanceFromBottom: number): boolean {
  return distanceFromBottom < 24;
}

function sessionStore(): AnchorStore | null {
  if (typeof sessionStorage === 'undefined') return null;
  return sessionStorage;
}

/**
 * The transcript, windowed.
 *
 * `Bubble.List` renders every child and has no virtualisation — with tool cards
 * expanded that is thousands of DOM nodes in a long session, so rows are
 * windowed here with dynamic measurement (WEB-CLIENT §7.1) and each row is a
 * plain `Bubble`, keeping one visual language.
 *
 * Follow rules (§7.2): stick to the bottom while the reader is within 24px of
 * it; the moment they scroll up, following stops and a single "回到最新" action
 * appears. A reader's position is never stolen.
 *
 * Restoration (A3): each session remembers (anchored message id + offset) and
 * whether the reader was following. Returning to a followed session keeps
 * following; returning to a detached one restores that row and offset.
 */
export function Transcript({
  messages,
  sessionId,
  focusIndex,
}: {
  messages: MessageInfo<ZeoMessage>[];
  /** Needed for per-session restoration; omitted where a test does not care. */
  sessionId?: string;
  /** Scroll target requested by the find bar. */
  focusIndex?: number;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  // Follow state is decided *before* the first commit: a session left detached
  // must not flash the bottom (or scroll there) while its anchor is restored.
  const [stick, setStick] = useState(() => {
    if (!sessionId) return true;
    const store = sessionStore();
    const anchor = store ? loadAnchor(store, sessionId) : null;
    return !anchor || anchor.atBottom;
  });
  const stickRef = useRef(stick);
  stickRef.current = stick;
  const restoredFor = useRef<string | null>(null);
  const lastSave = useRef(0);
  // The rows actually on screen, so the anchor can be captured before (or
  // without) the virtualizer's own measurement.

  const virtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: 8,
    // jsdom and the first paint have no measured viewport; this keeps a
    // sensible window instead of rendering nothing.
    initialRect: { width: 800, height: 800 },
  });

  /**
   * Whether the scroller has a real layout. In a browser it always does; in a
   * layout-less environment (jsdom) every virtualizer offset is meaningless, so
   * the estimate grid becomes the shared coordinate system instead.
   */
  const hasLayout = () => (scrollRef.current?.getBoundingClientRect().height ?? 0) > 0;

  /** The virtualizer's own row offsets, or null before it has measured. */
  const offsetOf = (index: number): number | null => {
    if (!hasLayout()) return null;
    const target = virtualizer.getOffsetForIndex(index, 'start');
    if (!target) return null;
    return Number.isFinite(target[0]) ? target[0] : null;
  };

  /**
   * Anchor the current position to a message id, not to a pixel offset.
   *
   * Capture and restore use the same coordinate source (the virtualizer's
   * offsets, or the estimate grid when it has not measured yet), so a captured
   * (id, offset) lands back exactly where the reader left.
   */
  const captureAnchor = (atBottom: boolean) => {
    const element = scrollRef.current;
    if (!element || messages.length === 0) return null;
    const { index, start } = indexAtOffset({
      count: messages.length,
      scrollTop: element.scrollTop,
      estimate: ESTIMATED_ROW_HEIGHT,
      offsetOf,
      measured: hasLayout(),
    });
    const message = messages[index];
    if (!message) return null;
    return {
      messageId: String(message.id),
      offset: Math.max(0, Math.round(element.scrollTop - start)),
      atBottom,
    };
  };

  const persist = (atBottom: boolean, force = false) => {
    if (!sessionId) return;
    const store = sessionStore();
    if (!store) return;
    const now = Date.now();
    if (!force && now - lastSave.current < SAVE_THROTTLE_MS) return;
    lastSave.current = now;
    const anchor = captureAnchor(atBottom);
    if (anchor) saveAnchor(store, sessionId, anchor);
  };

  useEffect(() => {
    const element = scrollRef.current;
    if (!element) return;
    const onScroll = () => {
      const distance = element.scrollHeight - element.scrollTop - element.clientHeight;
      const following = shouldFollow(distance);
      setStick(following);
      persist(following);
    };
    element.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      element.removeEventListener('scroll', onScroll);
      // Leaving the session: record the exact position, not a throttled one.
      persist(stickRef.current, true);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- persist reads live refs
  }, [sessionId, messages.length]);

  // Restore once per session, after the rows exist.
  useEffect(() => {
    if (!sessionId || messages.length === 0) return;
    if (restoredFor.current === sessionId) return;
    restoredFor.current = sessionId;
    const store = sessionStore();
    const anchor = store ? loadAnchor(store, sessionId) : null;
    const resolved = resolveAnchor(anchor, messages);
    if (resolved.atBottom) {
      setStick(true);
      virtualizer.scrollToIndex(messages.length - 1, { align: 'end' });
      return;
    }
    setStick(false);
    const start = offsetOf(resolved.index);
    if (start !== null) {
      virtualizer.scrollToOffset(start + resolved.offset);
    } else {
      virtualizer.scrollToOffset(resolved.index * ESTIMATED_ROW_HEIGHT + resolved.offset);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- restore is once per session
  }, [sessionId, messages.length, virtualizer]);

  // Keep the newest row in view while the reader is at the bottom. `newest`
  // identity changes on every streamed delta — exactly when follow must run.
  const newest = messages[messages.length - 1];
  useEffect(() => {
    if (!stick || messages.length === 0) return;
    virtualizer.scrollToIndex(messages.length - 1, { align: 'end' });
  }, [newest, stick, messages.length, virtualizer]);

  useEffect(() => {
    if (focusIndex === undefined) return;
    virtualizer.scrollToIndex(focusIndex, { align: 'center' });
  }, [focusIndex, virtualizer]);

  // With no measurable viewport (jsdom, or the first paint before layout) the
  // virtualizer reports nothing; fall back to a bounded tail window so the
  // transcript is never blank and never unbounded. A real browser measures on
  // the first frame and the virtualizer takes over.
  const measured = virtualizer.getVirtualItems();
  const rows =
    measured.length > 0
      ? measured
      : messages.slice(Math.max(0, messages.length - FALLBACK_ROWS)).map((_, offset) => {
          const index = Math.max(0, messages.length - FALLBACK_ROWS) + offset;
          return { index, key: `fallback-${offset}`, start: index * ESTIMATED_ROW_HEIGHT };
        });

  return (
    <div className="zk-transcript">
      <div className="zk-transcript__scroll" ref={scrollRef} role="log" aria-live="polite">
        <div className="zk-transcript__inner" style={{ height: virtualizer.getTotalSize() }}>
          {rows.map((row) => {
            const info = messages[row.index];
            if (!info) return null;
            const isUser = info.message.role === 'user';
            return (
              <div
                key={row.key}
                data-index={row.index}
                data-message-id={String(info.id)}
                ref={virtualizer.measureElement}
                className="zk-transcript__row"
                style={{ transform: `translateY(${row.start}px)` }}
              >
                <Bubble
                  placement={isUser ? 'end' : 'start'}
                  variant={isUser ? 'filled' : 'borderless'}
                  content={info.message}
                  loading={info.status === 'loading'}
                  contentRender={(content) =>
                    isUser ? (
                      <span className="zk-user-text">{(content as ZeoMessage).text}</span>
                    ) : (
                      <AssistantBody message={content as ZeoMessage} status={info.status} />
                    )
                  }
                />
              </div>
            );
          })}
        </div>
      </div>
      {stick ? null : (
        <div className="zk-transcript__jump">
          <Button
            size="small"
            onClick={() => {
              // Resume following at once; the scroll event confirms it.
              setStick(true);
              persist(true, true);
              virtualizer.scrollToIndex(messages.length - 1, { align: 'end' });
            }}
          >
            回到最新
          </Button>
        </div>
      )}
    </div>
  );
}