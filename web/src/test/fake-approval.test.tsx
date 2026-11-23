import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * `approval.resolve` must honour its `decision` (docs/TRANSPORT.md §5.1.3).
 *
 * The fixture used to delete the approval by id and ignore whether the client
 * said allow or deny — which made the two verbs the same code path. Every
 * approval test then proved "the card disappeared", never "the tool did not run".
 * AGENTS §3 requires the vocabulary to stay non-neutral; the test double must not
 * neutralise it on the other side.
 */

let gateway: FakeGatewayHandle | null = null;

beforeEach(() => {
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway?.dispose();
  gateway = null;
});

function renderWithApproval() {
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [] };
  state.approvals = [
    {
      id: 'appr-1',
      tool: 'bash',
      summary: 'npm run build',
      command: 'npm run build',
      sessionId: 's-1',
      requestedAt: '2026-11-23T00:00:00Z',
    } as never,
  ];
  gateway = installFakeGateway(state);
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={['/c/s-1']}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

describe('approval.resolve keeps allow and deny distinct', () => {
  it('allows: records an allow decision and runs the tool', async () => {
    const user = renderWithApproval();
    await user.click(await screen.findByTestId('approval-allow'));

    await waitFor(() => {
      const fake = gateway?.gateway;
      expect(fake?.decisions).toEqual([{ id: 'appr-1', tool: 'bash', decision: 'allow' }]);
      expect(fake?.allowedTools).toEqual(['bash']);
      // A denial is a result too: nothing may end up in the denied list.
      expect(fake?.deniedTools).toEqual([]);
    });
  }, 20_000);

  it('denies: records a deny decision and the tool never runs', async () => {
    const user = renderWithApproval();
    // Located by test id: antd renders the two-character label as 「拒 绝」.
    await user.click(await screen.findByTestId('approval-deny'));

    await waitFor(() => {
      const fake = gateway?.gateway;
      expect(fake?.decisions).toEqual([{ id: 'appr-1', tool: 'bash', decision: 'deny' }]);
      // The point of the whole fix: deny is not "the card went away".
      expect(fake?.deniedTools).toEqual(['bash']);
      expect(fake?.allowedTools).toEqual([]);
    });
  }, 20_000);

  it('rejects a decision outside the contract vocabulary', async () => {
    renderWithApproval();
    await screen.findByTestId('approval-allow');
    const fake = gateway!.gateway;
    // `approval_resolve` in Go answers invalid_params for anything else; the
    // fixture must not be more permissive than the gateway. Driven through
    // `receive` — the same entry point the socket uses — so it exercises the
    // real dispatch rather than a test-only shortcut.
    const replies: Array<Record<string, unknown>> = [];
    const fakeSocket = {
      push: (frame: Record<string, unknown>) => replies.push(frame),
      closed: false,
    } as never;
    fake.receive(fakeSocket, {
      jsonrpc: '2.0',
      id: 99,
      method: 'approval.resolve',
      params: { id: 'appr-1', decision: 'maybe' },
    });
    expect(replies.map((frame) => (frame.error as { code?: number } | undefined)?.code)).toContain(
      -32602,
    );
    // Nothing happened: the approval is still pending, no decision recorded.
    expect(fake.decisions).toEqual([]);
    expect(fake.state.approvals).toHaveLength(1);
  }, 20_000);
});
