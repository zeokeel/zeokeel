import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { InspectorPanel } from '@/features/inspector/inspector-panel';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * B13 (queue as a collapsed chip) and C5 (workspace-root breadcrumb).
 *
 * Both are "polish" items with a real cost behind them:
 *  - the always-on queue panel held an extra block over the composer while idle
 *    and fought the accent-fill budget, so the list moved into a popover whose
 *    trigger carries the count;
 *  - the inspector's root crumb must go **back to the tree and keep the tab
 *    open** — closing the preview would drop the place the user was reading.
 */

let gateway: FakeGatewayHandle;

beforeEach(() => {
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

function renderAt(path: string) {
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

describe('B13 — the queue is a collapsed chip', () => {
  it('shows the count on the chip and reveals the rows only when opened', async () => {
    const state = defaultState();
    state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
    state.messages = { 's-1': [] };
    state.prompts = { 's-1': [{ id: 'q-1', text: '排队中' }] };
    gateway = installFakeGateway(state);

    renderAt('/c/s-1');
    const chip = await screen.findByRole('button', { name: '1 条待发送' });
    expect(chip).toBeInTheDocument();
    // Collapsed: the row text is not rendered yet (it lives in the popover).
    expect(screen.queryByText('排队中')).toBeNull();

    await userEvent.click(chip);
    expect(await screen.findByText('排队中')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '取消待发送：排队中' })).toBeInTheDocument();
  });

  it('keeps the send-head affordance inside the popover when idle', async () => {
    const state = defaultState();
    state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
    state.messages = { 's-1': [] };
    state.prompts = { 's-1': [{ id: 'q-1', text: '剩一条' }] };
    gateway = installFakeGateway(state);

    const user = renderAt('/c/s-1');
    await user.click(await screen.findByRole('button', { name: '1 条待发送' }));
    expect(await screen.findByRole('button', { name: '发送队首' })).toBeInTheDocument();
  });
});

describe('C5 — the workspace root crumb returns to the tree', () => {
  it('navigates to the tree and keeps the inspector mounted', async () => {
    // Render the route shape the app uses (`/files/*path`), so `useParams()['*']`
    // resolves and the preview branch — with its breadcrumb — is on screen.
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/c/s-1/files/src/chat/composer.tsx']}>
        <Routes>
          <Route path="/c/:sessionId/files/*" element={<InspectorPanel />} />
          <Route path="/c/:sessionId/files" element={<InspectorPanel />} />
        </Routes>
      </MemoryRouter>,
    );

    // The preview is open and carries the breadcrumb.
    const inspector = await screen.findByRole('complementary');
    const crumb = document.querySelector('.zk-inspector__crumb') as HTMLElement;
    expect(crumb).toBeTruthy();
    expect(crumb.textContent).toContain('工作区根目录');
    expect(document.querySelector('.zk-inspector__path')?.textContent).toBe('src/chat/composer.tsx');

    await user.click(crumb);

    // Back to the tree: the file path is gone, the panel is still mounted — the
    // tab was not closed (that is the point of C5).
    await waitFor(() =>
      expect(document.querySelector('.zk-inspector__path')).toBeNull(),
    );
    expect(document.body.contains(inspector)).toBe(true);
    expect(screen.getByRole('complementary')).toBeInTheDocument();
  });
});

describe('file routes actually match', () => {
  it('renders the inspector for /c/:id/files/<path> (splat, not *path)', async () => {
    // Regression: the route was registered as `*path`, which react-router reads
    // as a literal segment — every file URL fell through to NotFound and the
    // inspector could never be reached from a deep link.
    render(
      <MemoryRouter initialEntries={['/c/s-1/files/src/chat/composer.tsx']}>
        <App />
      </MemoryRouter>,
    );
    // The sidebar is also a complementary landmark, so key off the inspector.
    await waitFor(() => expect(document.querySelector('.zk-inspector')).not.toBeNull());
    expect(screen.queryByText('这个地址没有页面')).toBeNull();
  });
});
