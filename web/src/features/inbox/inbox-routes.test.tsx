import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * Two inboxes, two routes (WEB-CLIENT §1.4 vs TRANSPORT §5.1.13).
 *
 * `/inbox` is the approvals / ask / plan / artifact aggregation — it shipped
 * first and its copy and in-place decisions must not move. `/inbox/incoming` is
 * the gateway's inbound work queue. This suite pins the split: neither copy may
 * appear on the other route, so swapping the two components would fail here.
 */
let gateway: FakeGatewayHandle;

function mount(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  gateway = installFakeGateway(defaultState());
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

describe('/inbox — the approvals aggregation', () => {
  it('keeps its own empty copy and does not render the work inbox', async () => {
    mount('/inbox');

    // The count line and the Empty component both carry the old copy; either
    // way, this string is the approvals inbox's, not the work queue's.
    expect(await screen.findAllByText('没有等你处理的事')).not.toHaveLength(0);
    expect(screen.queryByText('没有等你的。')).toBeNull();
    expect(screen.queryByText('现在必须做')).toBeNull();
  });

  it('still decides a pending approval in place', async () => {
    const user = userEvent.setup();
    gateway.dispose();
    const state = defaultState();
    state.approvals = [
      { id: 'a-1', tool: 'bash', summary: '删除构建目录', sessionId: 's-1' },
    ];
    gateway = installFakeGateway(state);

    mount('/inbox');

    expect(await screen.findByText('删除构建目录')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /拒\s*绝/ }));

    // The decision reached the gateway as "deny", not merely "the row vanished".
    await waitFor(() =>
      expect(gateway.gateway.decisions).toContainEqual({
        id: 'a-1',
        tool: 'bash',
        decision: 'deny',
      }),
    );
    expect(screen.queryByText('删除构建目录')).toBeNull();
  });
});

describe('/inbox/incoming — the inbound work inbox', () => {
  it('is wired to the work screen, not to the approvals one', async () => {
    mount('/inbox/incoming');

    expect(await screen.findByText('没有等你的。')).toBeInTheDocument();
    expect(screen.queryByText('没有等你处理的事')).toBeNull();
  });

  it('turns a row into a task through the real client path', async () => {
    const user = userEvent.setup();
    gateway.dispose();
    const state = defaultState();
    state.inbox = [
      {
        id: 'inbox-1',
        source: 'wechat',
        title: '查退款率',
        status: 'new',
        count: 1,
        receivedAt: new Date().toISOString(),
      },
    ];
    gateway = installFakeGateway(state);

    mount('/inbox/incoming');

    await user.click(await screen.findByRole('button', { name: '派成任务' }));

    await waitFor(() =>
      expect(gateway.gateway.dispatches).toEqual([
        { id: 'inbox-1', subject: '查退款率', taskIds: ['task-inbox-1'] },
      ]),
    );
    // The re-read moves the row to 已分诊 — the write and the list agree.
    expect(await screen.findByRole('region', { name: '已分诊' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: '现在必须做' })).toBeNull();
  });
});