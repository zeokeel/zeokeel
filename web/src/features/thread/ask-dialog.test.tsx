import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * R5-adjacent: the in-conversation ask (native 「需要你的确认」).
 *
 * The ask used to be a read-only inbox row — the model could ask and nobody could
 * answer from the browser. This pins the answer path: option click, custom answer,
 * cancel and Esc.
 */

let gateway: FakeGatewayHandle;
let calls: Array<{ method: string; params: Record<string, unknown> }>;

beforeEach(() => {
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [] };
  state.ask = {
    id: 'ask-1',
    text: '要使用哪个数据库？',
    options: ['SQLite（本地文件）', 'PostgreSQL（服务端）'],
  };
  gateway = installFakeGateway(state);
  calls = [];
  const originalReceive = gateway.gateway.receive.bind(gateway.gateway);
  gateway.gateway.receive = (socket, message) => {
    if (message.method === 'ask.answer' || message.method === 'ask.cancel') {
      calls.push({ method: message.method, params: (message.params ?? {}) as Record<string, unknown> });
    }
    originalReceive(socket, message);
  };
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

const open = () =>
  render(
    <MemoryRouter initialEntries={['/c/s-1']}>
      <App />
    </MemoryRouter>,
  );

describe('ask dialog', () => {
  it('shows the question with its options and answers with the picked one', async () => {
    const user = userEvent.setup();
    open();

    expect(await screen.findByText('要使用哪个数据库？')).toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: 'PostgreSQL（服务端）' }));

    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toEqual({
      method: 'ask.answer',
      params: { id: 'ask-1', answer: 'PostgreSQL（服务端）' },
    });
    await waitFor(() => expect(screen.queryByText('要使用哪个数据库？')).not.toBeInTheDocument());
  });

  it('answers a custom reply through 「其他…」', async () => {
    const user = userEvent.setup();
    open();

    await user.click(await screen.findByRole('button', { name: /其他…/ }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByRole('textbox'), '用 DuckDB 试试');
    await user.click(screen.getByRole('button', { name: /提\s*交/ }));

    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toEqual({ method: 'ask.answer', params: { id: 'ask-1', answer: '用 DuckDB 试试' } });
  });

  it('cancels with Esc without answering', async () => {
    open();
    expect(await screen.findByText('要使用哪个数据库？')).toBeInTheDocument();

    await userEvent.setup().keyboard('{Escape}');

    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0].method).toBe('ask.cancel');
    expect(calls[0].params).toEqual({ id: 'ask-1' });
  });
});