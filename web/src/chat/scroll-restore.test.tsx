import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { anchorStorageKey } from './scroll-anchor';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * A3 — each session restores its own reading position.
 *
 * Three round trips between two sessions: A is left scrolled up (detached),
 * B is left at the bottom (following). A must come back to its row and stay
 * detached, B must come back following. Nothing may go blank or jump to the
 * top while the virtualised window is rebuilt.
 */

const ROW_ESTIMATE = 140;
const VIEWPORT = 600;
const SCROLL_HEIGHT = 9000;
// Inside the tail window jsdom renders when nothing is measured (rows 20..59).
const DETACHED_TOP = 50 * ROW_ESTIMATE + 40;

function messagesFor(sessionId: string, count = 60) {
  return Array.from({ length: count }, (_, index) => ({
    id: `${sessionId}-m-${index}`,
    role: index % 2 === 0 ? 'user' : 'assistant',
    text: `${sessionId} 的第 ${index} 条`,
  }));
}

let handle: FakeGatewayHandle;

beforeEach(() => {
  sessionStorage.clear();
  const state = defaultState();
  state.sessions = [
    { id: 'sa', title: '会话 A', updatedAt: '2026-11-23T00:00:00Z' },
    { id: 'sb', title: '会话 B', updatedAt: '2026-11-23T00:00:00Z' },
  ];
  state.messages = { sa: messagesFor('sa'), sb: messagesFor('sb') } as never;
  handle = installFakeGateway(state);
  // jsdom has no layout. Patch the prototype (not one element) so the
  // transcript that mounts *during the switch* already sees a viewport; the
  // restore effect runs on mount, long before a test could touch the element.
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get() {
      return SCROLL_HEIGHT;
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get() {
      return VIEWPORT;
    },
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    configurable: true,
    value(this: HTMLElement, options?: ScrollToOptions) {
      if (typeof options?.top === 'number') this.scrollTop = options.top;
    },
  });
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  handle.dispose();
});

function currentScroller(): HTMLElement {
  return document.querySelector('.zk-transcript__scroll') as HTMLElement;
}

function scrollTo(scroller: HTMLElement, top: number) {
  scroller.scrollTop = top;
  fireEvent.scroll(scroller);
}

async function openSession(user: ReturnType<typeof userEvent.setup>, title: string) {
  // The title appears in the sidebar *and* in the top band; switch via the row.
  const sidebar = document.querySelector('.zk-sidebar') as HTMLElement;
  await user.click(await within(sidebar).findByText(title));
  // The transcript of the new session has rendered once rows exist.
  await waitFor(() => expect(document.querySelector('.zk-transcript__scroll')).toBeTruthy());
  return currentScroller();
}

describe('per-session scroll restoration (A3)', () => {
  it('restores each session position and follow state across three round trips', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/c/sa']}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole('textbox');

    // ---- session A: leave detached, anchored at row 14 + 40px ------------
    let scrollerA = await openSession(user, '会话 A');
    scrollTo(scrollerA, DETACHED_TOP);
    await waitFor(() => {
      const stored = sessionStorage.getItem(anchorStorageKey('sa'));
      expect(stored).toBeTruthy();
      expect(JSON.parse(stored!)).toMatchObject({ offset: 40, atBottom: false });
    });

    // ---- session B: leave following the bottom ---------------------------
    let scrollerB = await openSession(user, '会话 B');
    scrollTo(scrollerB, SCROLL_HEIGHT - VIEWPORT);
    await waitFor(() =>
      expect(JSON.parse(sessionStorage.getItem(anchorStorageKey('sb'))!)).toMatchObject({
        atBottom: true,
      }),
    );

    for (let round = 0; round < 3; round += 1) {
      // Back to A: the anchored row and offset, still detached.
      scrollerA = await openSession(user, '会话 A');
      await waitFor(() => expect(scrollerA.scrollTop).toBe(DETACHED_TOP));
      expect(screen.getByRole('button', { name: '回到最新' })).toBeInTheDocument();
      // The window is rebuilt, not blank: a row is actually rendered.
      expect(document.querySelectorAll('.zk-transcript__row').length).toBeGreaterThan(0);

      // Back to B: following, no jump button, at the bottom.
      scrollerB = await openSession(user, '会话 B');
      await waitFor(() => expect(scrollerB.scrollTop).toBe(SCROLL_HEIGHT - VIEWPORT));
      expect(screen.queryByRole('button', { name: '回到最新' })).not.toBeInTheDocument();
      expect(document.querySelectorAll('.zk-transcript__row').length).toBeGreaterThan(0);
    }
  }, 30_000);

  it('follows the newest message when the session was left at the bottom', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/c/sb']}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole('textbox');

    const scrollerB = await openSession(user, '会话 B');
    scrollTo(scrollerB, SCROLL_HEIGHT - VIEWPORT);
    expect(screen.queryByRole('button', { name: '回到最新' })).not.toBeInTheDocument();

    await openSession(user, '会话 A');
    const restored = await openSession(user, '会话 B');
    await waitFor(() => expect(restored.scrollTop).toBe(SCROLL_HEIGHT - VIEWPORT));
    // Following again means the newest message is in the rendered window.
    expect(restored.querySelector('[data-message-id="sb-m-59"]')).toBeTruthy();
  }, 20_000);
});