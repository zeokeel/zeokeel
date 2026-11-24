import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { applyTasksDelta, activeTaskCount, normalizeTask, toTaskStatus } from '@/api/tasks';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * B12 — the background-task chip (docs/TRANSPORT.md §5.1.8).
 *
 *  - the board is `tasks` (process) or `tasks:<id>` (per session);
 *  - `tasks.delta.added` is an **upsert by id**, not a field-level patch — that
 *    is the mistake this file exists to prevent;
 *  - 停止 is `task.status {status:"pending"}`; 取消 is `task.delete`;
 *  - an empty board shows no chip at all (never a `0`).
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

const task = (overrides: Record<string, unknown> = {}) => ({
  id: 'task-1',
  subject: '复核 B5',
  status: 'in_progress',
  ...overrides,
});

function renderThread(tasks: Array<Record<string, unknown>> = [task()]) {
  const state = defaultState();
  state.tasks = { '': tasks };
  state.taskRevs = { '': 1 };
  gateway = installFakeGateway(state);
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={['/c/s-1']}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

describe('tasks delta is an upsert, not a patch', () => {
  it('replaces a whole row by id and keeps the order', () => {
    const first = [normalizeTask({ id: 'a', subject: 'one', status: 'pending' })];
    const next = applyTasksDelta(first, {
      // Only `status` changed on the wire, but the row arrives whole.
      added: [{ id: 'a', subject: 'one', status: 'completed' }],
      removed: [],
    });
    expect(next).toHaveLength(1);
    expect(next[0].status).toBe('completed');

    // A partial row would otherwise erase the subject; `added` is the truth for
    // the fields it carries, and the row it carries is the whole row.
    const replaced = applyTasksDelta(next, {
      added: [{ id: 'a', subject: 'one (renamed)', status: 'completed' }],
    });
    expect(replaced[0].subject).toBe('one (renamed)');
  });

  it('appends new ids and drops removed ones', () => {
    const next = applyTasksDelta(
      [normalizeTask({ id: 'a', subject: 'one' })],
      { added: [{ id: 'b', subject: 'two' }], removed: ['a'] },
    );
    expect(next.map((item) => item.id)).toEqual(['b']);
  });

  it('counts only unfinished work', () => {
    expect(
      activeTaskCount([
        normalizeTask({ id: 'a', status: 'in_progress' }),
        normalizeTask({ id: 'b', status: 'pending' }),
        normalizeTask({ id: 'c', status: 'completed' }),
      ]),
    ).toBe(2);
  });

  it('maps protocol statuses without inventing a fourth state', () => {
    expect(toTaskStatus('in_progress')).toBe('in_progress');
    expect(toTaskStatus('completed')).toBe('completed');
    expect(toTaskStatus('pending')).toBe('pending');
    expect(toTaskStatus('whatever')).toBe('pending');
  });
});

describe('B12 — the chip', () => {
  it('shows the unfinished count and nothing when the board is empty', async () => {
    const user = renderThread();
    const chip = await screen.findByRole('button', { name: /1 进行中 · 0 待处理/ });
    expect(chip).toBeInTheDocument();

    await user.click(chip);
    await waitFor(() => expect(document.querySelector('.zk-tasks__row')).not.toBeNull());
    // Scoped to the popover: the sidebar is a list too.
    const popover = document.querySelector('.ant-popover') as HTMLElement;
    expect(within(popover).getByText('复核 B5')).toBeInTheDocument();
  });

  it('renders no chip at all for an empty board', async () => {
    renderThread([]);
    await screen.findByRole('textbox');
    expect(screen.queryByRole('button', { name: /已完成 ·/ })).toBeNull();
  });

  it('stops a running task with status=pending and cancels with task.delete', async () => {
    const user = renderThread();
    await user.click(await screen.findByRole('button', { name: /1 进行中 · 0 待处理/ }));
    await waitFor(() => expect(document.querySelector('.ant-popover')).not.toBeNull());

    await user.click(screen.getByRole('button', { name: '停止' }));
    await waitFor(() => {
      const call = gateway?.gateway.calls.find((entry) => entry.method === 'task.status');
      // `pending` IS the stop state (§5.1.8) — there is no task.stop.
      expect(call?.params).toEqual({ id: 'task-1', status: 'pending' });
    });

    await user.click(screen.getByRole('button', { name: '取消' }));
    await waitFor(() => {
      const call = gateway?.gateway.calls.find((entry) => entry.method === 'task.delete');
      expect(call?.params).toEqual({ id: 'task-1' });
    });
  });

  it('follows a pushed delta (a change made elsewhere)', async () => {
    renderThread();
    await screen.findByRole('button', { name: /1 进行中 · 0 待处理/ });

    gateway?.gateway.notify('tasks.delta', {
      baseRev: 1,
      rev: 2,
      added: [{ id: 'task-2', subject: '另一个任务', status: 'in_progress' }],
      removed: [],
    });

    // task-1 was in_progress and the delta adds a second in-progress row.
    expect(await screen.findByRole('button', { name: /0 已完成 · 2 进行中 · 0 待处理/ })).toBeInTheDocument();
  });
});