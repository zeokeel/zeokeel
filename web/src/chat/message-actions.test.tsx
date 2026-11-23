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
    // The transcript virtualises, so wait for the row (and its actions) to land.
    await waitFor(() => expect(screen.getAllByRole('button', { name: '复制' }).length).toBe(2));

    await userEvent.click(screen.getByRole('button', { name: '更多操作' }));
    // The sidebar is a menu too, so scope to the dropdown popup.
    await waitFor(() => expect(document.querySelector('.ant-dropdown')).not.toBeNull());
    const menu = document.querySelector('.ant-dropdown') as HTMLElement;
    expect(within(menu).getByText('重新生成')).toBeInTheDocument();
    // An answer cannot be edited (§4.6 `edit_target_not_user`): 编辑 lives on
    // the user row only, so the assistant menu never offers it.
    expect(within(menu).queryByText('编辑')).toBeNull();
  });

  it('gives the user bubble an edit action and no regenerate', async () => {
    renderThread();
    await screen.findByText('帮我看下 transport');
    // Two rows, two copy buttons; only the user row offers 编辑.
    await waitFor(() =>
      expect(screen.getAllByRole('button', { name: '复制' })).toHaveLength(2),
    );
    const edits = screen.getAllByRole('button', { name: '编辑' });
    expect(edits).toHaveLength(1);
    expect(screen.getAllByRole('button', { name: '更多操作' })).toHaveLength(1);
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

describe('B11 — edit goes through the same endpoint, third mode', () => {
  it('sends editMessageId with the new text and no regenerate flag', async () => {
    const { GatewayChatProvider } = await import('@/runtime/gateway-chat-provider');
    const provider = new GatewayChatProvider();
    const body = provider.transformParams(
      { sessionId: 's-1', editMessageId: 'm-1', text: '改过的提问', clientRunId: 'run-9' },
      { params: {} } as never,
    );
    expect(body).toEqual({
      sessionId: 's-1',
      text: '改过的提问',
      editMessageId: 'm-1',
      clientRunId: 'run-9',
    });
    expect('regenerate' in body).toBe(false);
  });

  it('refuses an empty edit text: an edit replaces, it does not delete', async () => {
    const { GatewayChatProvider } = await import('@/runtime/gateway-chat-provider');
    const provider = new GatewayChatProvider();
    expect(() =>
      provider.transformParams(
        { sessionId: 's-1', editMessageId: 'm-1', text: '   ', clientRunId: 'r' },
        { params: {} } as never,
      ),
    ).toThrow(/requires text for an edit/);
  });

  it('adds no local user bubble for an edit (history comes from msg.reset)', async () => {
    const { GatewayChatProvider } = await import('@/runtime/gateway-chat-provider');
    const provider = new GatewayChatProvider();
    const local = provider.transformLocalMessage({
      sessionId: 's-1',
      editMessageId: 'm-1',
      text: '改过的提问',
    });
    expect(local.role).toBe('assistant');
    expect(local.text).toBe('');
  });

  it('recognises all five refusal names and nothing else', async () => {
    const { editRefusalOf, EDIT_REFUSALS } = await import('@/runtime/gateway-chat-provider');
    for (const name of EDIT_REFUSALS) {
      expect(editRefusalOf({ data: { name } })).toBe(name);
    }
    expect(editRefusalOf({ data: { name: 'something_else' } })).toBeNull();
    expect(editRefusalOf(undefined)).toBeNull();
  });
});

describe('B11 — a refused edit disables the action with its reason', () => {
  it('renders 编辑 greyed out and carries the reason in its tooltip', async () => {
    const { MessageActions } = await import('@/chat/message-actions');
    const { render: renderInline } = await import('@testing-library/react');
    renderInline(
      <MessageActions
        role="user"
        text="原本的话"
        onEdit={() => undefined}
        editDisabled
        editHint="这条消息已被压缩出历史窗口"
      />,
    );
    const button = screen.getByRole('button', { name: '编辑' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', '这条消息已被压缩出历史窗口');
  });
});
