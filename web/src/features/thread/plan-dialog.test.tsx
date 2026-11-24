import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * The plan review (native 「计划待审阅」). `plan.exit` existed in the gateway and the
 * client never sent it, so a submitted plan parked the run with no way out from the
 * browser. This pins approve, cancel and revise-with-feedback.
 */

let gateway: FakeGatewayHandle;
let calls: Array<{ method: string; params: Record<string, unknown> }>;

beforeEach(() => {
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [] };
  state.plan = { state: 'reviewing', content: '发布计划\n1. 运行全量测试\n2. 更新文档\n3. 打 tag' };
  gateway = installFakeGateway(state);
  calls = [];
  const originalReceive = gateway.gateway.receive.bind(gateway.gateway);
  gateway.gateway.receive = (socket, message) => {
    if (message.method === 'plan.exit') {
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

describe('plan dialog', () => {
  it('shows the plan and approves it', async () => {
    const user = userEvent.setup();
    open();

    expect(await screen.findByText(/发布计划/)).toBeInTheDocument();
    await user.click(screen.getByTestId('plan-approve'));

    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toEqual({ method: 'plan.exit', params: { action: 'approve' } });
    // Review does not resume the run: approving starts the execution as a turn.
    await waitFor(() => expect(screen.getByText(/计划已批准，开始执行/)).toBeInTheDocument());
  });

  it('cancels the plan', async () => {
    const user = userEvent.setup();
    open();

    await screen.findByText(/发布计划/);
    await user.click(screen.getByRole('button', { name: /取消计划/ }));

    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toEqual({ method: 'plan.exit', params: { action: 'cancel' } });
  });

  it('sends a revision as a message instead of a plan action', async () => {
    const user = userEvent.setup();
    open();

    await screen.findByText(/发布计划/);
    const field = screen.getByPlaceholderText('告诉 agent 计划需要怎么改…');
    await user.type(field, '第二步先更新 CHANGELOG');
    await user.click(screen.getByRole('button', { name: /继续规划/ }));

    // No plan.exit: the revision is a normal turn.
    expect(calls).toHaveLength(0);
    await waitFor(() =>
      expect(screen.getByText('第二步先更新 CHANGELOG')).toBeInTheDocument(),
    );
  });
});
