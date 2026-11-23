import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * B14 — toasts (docs/TRANSPORT.md §6.3).
 *
 * The shape is the gateway's, not ours: `toast.posted {id, level, text,
 * sessionId?, actionLabel?, actionId?}`. What the client contributes is the two
 * rules that make a toast trustworthy:
 *
 *  - a receipt disappears after 4 seconds;
 *  - an error does **not** — it waits for the user, or failures get missed.
 *
 * An action button answers `notify.action {id, actionId}`; the gateway keeps the
 * closure, the client never executes it locally.
 */

let gateway: FakeGatewayHandle;

beforeEach(() => {
  const state = defaultState();
  gateway = installFakeGateway(state);
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  gateway.dispose();
});

function renderApp(path = '/c/s-1') {
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

const toast = (overrides: Record<string, unknown> = {}) => ({
  id: 'toast-1',
  level: 'info',
  text: '这一轮已完成',
  ...overrides,
});

describe('B14 — toast.posted', () => {
  it('renders the text and sends notify.action when the action is clicked', async () => {
    renderApp();
    await screen.findByRole('textbox');

    gateway.gateway.notify(
      'toast.posted',
      toast({ actionLabel: '撤销', actionId: 'undo-1' }),
    );

    expect(await screen.findByText('这一轮已完成')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '撤销' }));

    await waitFor(() => {
      const call = gateway.gateway.calls.find(
        (entry) => entry.method === 'notify.action' && entry.params.actionId === 'undo-1',
      );
      expect(call).toBeTruthy();
      expect(call?.params).toEqual({ id: 'toast-1', actionId: 'undo-1' });
    });
  });

  it('does not stack two frames with the same id', async () => {
    renderApp();
    await screen.findByRole('textbox');

    const frame = toast({ text: '同一个回执' });
    gateway.gateway.notify('toast.posted', frame);
    gateway.gateway.notify('toast.posted', frame);

    await waitFor(() => expect(screen.getAllByText('同一个回执')).toHaveLength(1));
  });

  it('keeps an error on screen instead of letting it vanish', async () => {
    renderApp();
    await screen.findByRole('textbox');

    gateway.gateway.notify(
      'toast.posted',
      toast({ id: 'toast-err', level: 'error', text: '模型服务不可用' }),
    );
    expect(await screen.findByText('模型服务不可用')).toBeInTheDocument();

    // Wait past the 4s receipt lifetime; the error must still be there.
    await new Promise((resolve) => setTimeout(resolve, 4600));
    expect(screen.getByText('模型服务不可用')).toBeInTheDocument();
  }, 15_000);
});