import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * B11 — the message `⋯` menu (docs/TRANSPORT.md §4.6).
 *
 *  - 复制 is client-side;
 *  - 重新生成 is `POST /api/chat/run {regenerate:true}` — the same single send
 *    path as a normal turn, with **no `text`** (a stray text is `invalid_params`);
 *  - 编辑 does not exist in the protocol, so the menu does not offer it. That is
 *    why B11 is recorded as 部分, not 完成.
 */

let gateway: FakeGatewayHandle;

beforeEach(() => {
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

function renderThread() {
  const user = userEvent.setup();
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = {
    's-1': [
      { id: 'm-1', role: 'user', text: '帮我看下 transport' },
      { id: 'm-2', role: 'assistant', text: '看完了，一共三层。' },
    ],
  };
  gateway = installFakeGateway(state);
  render(
    <MemoryRouter initialEntries={['/c/s-1']}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

describe('B11 — message actions', () => {
  it('offers copy and regenerate on an assistant message, and nothing else', async () => {
    renderThread();

    await screen.findByText('看完了，一共三层。');
    // The transcript virtualises, so wait for the row (and its footer) to land.
    const copy = await screen.findByRole('button', { name: '复制' });
    expect(copy).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '更多操作' }));
    // The sidebar is a menu too, so scope to the dropdown popup.
    await waitFor(() => expect(document.querySelector('.ant-dropdown')).not.toBeNull());
    const menu = document.querySelector('.ant-dropdown') as HTMLElement;
    expect(within(menu).getByText('重新生成')).toBeInTheDocument();
    // 编辑 has no protocol method — offering it would be a lie.
    expect(within(menu).queryByText('编辑')).toBeNull();
  });

  it('does not show actions on the user bubble', async () => {
    renderThread();
    await screen.findByText('帮我看下 transport');
    // One assistant row → exactly one copy button.
    await waitFor(() =>
      expect(screen.getAllByRole('button', { name: '复制' })).toHaveLength(1),
    );
  });
});
describe('B11 — regenerate uses the single run path', () => {
  it('sends regenerate:true with no text through POST /api/chat/run', async () => {
    // The transport is stub-able at the api boundary: `run-stream` is the only
    // place that may fetch, so the test asserts the request body it would send.
    const { GatewayChatProvider } = await import('@/runtime/gateway-chat-provider');
    const provider = new GatewayChatProvider();

    const body = provider.transformParams(
      { sessionId: 's-1', regenerate: true, clientRunId: 'run-1' },
      { params: {} } as never,
    );

    expect(body).toEqual({ sessionId: 's-1', clientRunId: 'run-1', regenerate: true });
    // §4.6: a stray `text` here is `invalid_params {field:"text"}`.
    expect('text' in body).toBe(false);
  });

  it('shows no empty user bubble for a regenerate turn', async () => {
    const { GatewayChatProvider } = await import('@/runtime/gateway-chat-provider');
    const provider = new GatewayChatProvider();
    const local = provider.transformLocalMessage({ sessionId: 's-1', regenerate: true });
    expect(local.role).toBe('assistant');
    expect(local.text).toBe('');
  });

  it('still requires text for a normal turn', async () => {
    const { GatewayChatProvider } = await import('@/runtime/gateway-chat-provider');
    const provider = new GatewayChatProvider();
    expect(() =>
      provider.transformParams({ sessionId: 's-1', clientRunId: 'r' }, { params: {} } as never),
    ).toThrow(/requires text/);
  });

  it('recognises the gateway saying regenerate is unsupported (§4.6)', async () => {
    const { isRegenerateUnsupported } = await import('@/runtime/gateway-chat-provider');
    expect(isRegenerateUnsupported({ message: 'capability_unsupported: regenerate_not_supported' })).toBe(true);
    expect(isRegenerateUnsupported({ message: 'network down' })).toBe(false);
  });
});
