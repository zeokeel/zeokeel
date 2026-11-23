import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * Esc with the slash menu open — the contract, not the implementation.
 *
 * Written **before** the menu becomes X's `Suggestion` (task-37 #1), on purpose:
 * the Esc chain must not depend on who renders the menu. These four assertions
 * are the cross-implementation contract, so the swap that follows has to keep
 * them green without editing them.
 *
 * Product rule, not a test detail: the menu opens when the draft **is** a command
 * (`/` at the start), so `草稿 /` is plain text and must not open it.
 *
 * Located by **role** (`listbox` / `option`), never by class name: class names
 * belong to whichever library renders the menu and are not our contract.
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

function renderThread() {
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [] };
  gateway = installFakeGateway(state);
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={['/c/s-1']}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

describe('Esc closes the slash menu first (E4 contract)', () => {
  it('opens on a draft that is a command, with selectable options', async () => {
    const user = renderThread();
    const input = await screen.findByRole('textbox');
    await user.type(input, '/');
    expect(await screen.findByRole('listbox')).toBeInTheDocument();
    expect(screen.queryAllByRole('option').length).toBeGreaterThan(0);
  });

  it('does not open for a slash inside plain text', async () => {
    const user = renderThread();
    const input = await screen.findByRole('textbox');
    await user.type(input, '草稿 /');
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('Tab completes the text without executing the command', async () => {
    const user = renderThread();
    const input = await screen.findByRole('textbox');
    await user.type(input, '/inb');
    await screen.findByRole('listbox');
    await user.keyboard('{Tab}');
    // K3: Tab completes, executes nothing, moves no focus.
    await waitFor(() => expect(input).toHaveValue('/inbox '));
    expect(document.activeElement).toBe(input);
  });

  it('Esc closes the menu and does nothing else: run alive, draft kept, no approval decided', async () => {
    const user = renderThread();
    const input = await screen.findByRole('textbox');
    await user.type(input, '/');
    await screen.findByRole('listbox');

    const cancelCalls = () =>
      gateway!.gateway.methodsCalled().filter((name) => name === 'run.cancel').length;
    const cancelsBefore = cancelCalls();

    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull());
    expect(cancelsBefore, 'precondition: no run was cancelled before Esc').toBe(0);
    expect(cancelCalls(), 'Esc 只关菜单，不停止运行').toBe(cancelsBefore);
    expect(
      gateway!.gateway.calls.some((entry) => entry.method === 'approval.resolve'),
      'Esc 只关菜单，不拒绝审批',
    ).toBe(false);
    expect(input, 'Esc 只关菜单，草稿原样保留').toHaveValue('/');
  });
});