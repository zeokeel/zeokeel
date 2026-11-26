import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { InboxItem, InboxSnapshot } from '@/api/inbox';
import {
  expectNoHorizontalOverflow,
  expectTapTarget,
  expectWrapsOrEllipsis,
  installViewport,
  PHONE_WIDTH,
  uninstallViewport,
} from '@/test/viewport';
import { InboxScreen } from './InboxScreen';

/**
 * The api module is the gateway boundary, so the screen tests mock it: what is
 * asserted here is what the screen asks for and what it shows, not the wire
 * framing (`inbox-api.test.ts` pins the method names and parameters).
 */
const api = vi.hoisted(() => ({
  loadInboxSnapshot: vi.fn(),
  dispatchInboxItem: vi.fn(),
  triageInboxItem: vi.fn(),
}));

vi.mock('@/api/inbox', () => ({
  ALL_INBOX_STATUSES: ['new', 'triaged', 'task', 'ignored', 'archived'],
  loadInboxSnapshot: api.loadInboxSnapshot,
  dispatchInboxItem: api.dispatchInboxItem,
  triageInboxItem: api.triageInboxItem,
}));

function snapshot(items: InboxItem[]): InboxSnapshot {
  return {
    items,
    stats: {
      total: items.length,
      new: items.filter((row) => row.status === 'new').length,
      triaged: items.filter((row) => row.status !== 'new').length,
    },
  };
}

function row(id: string, overrides: Partial<InboxItem> = {}): InboxItem {
  return { id, source: 'wechat', title: `标题 ${id}`, status: 'new', count: 1, ...overrides };
}

beforeEach(() => {
  vi.clearAllMocks();
  api.dispatchInboxItem.mockResolvedValue({ taskIds: ['task-9'], created: true });
  api.triageInboxItem.mockResolvedValue(row('inbox-1', { status: 'ignored' }));
});

afterEach(() => {
  uninstallViewport();
});

describe('InboxScreen', () => {
  it('shows a skeleton, not an empty state, while the first read is in the air', () => {
    api.loadInboxSnapshot.mockReturnValue(new Promise(() => {}));

    render(<InboxScreen />);

    expect(document.querySelector('.ant-skeleton')).not.toBeNull();
    // The honest loading state claims nothing about the queue.
    expect(screen.queryByText('没有等你的。')).toBeNull();
    expect(screen.queryByText('现在必须做')).toBeNull();
  });

  it('says 没有等你的。 when the queue is empty', async () => {
    api.loadInboxSnapshot.mockResolvedValue(snapshot([]));

    render(<InboxScreen />);

    expect(await screen.findByText('没有等你的。')).toBeInTheDocument();
    // No zero-count section headers either.
    expect(screen.queryByText('现在必须做')).toBeNull();
    expect(screen.queryByText('已分诊')).toBeNull();
  });

  it('groups rows into 现在必须做 and 已分诊 with source, count and relative time', async () => {
    const twoMinutesAgo = new Date(Date.now() - 2 * 60_000).toISOString();
    api.loadInboxSnapshot.mockResolvedValue(
      snapshot([
        row('inbox-1', {
          title: '查退款率',
          source: 'wechat',
          sender: 'owner',
          count: 3,
          receivedAt: twoMinutesAgo,
        }),
        row('inbox-2', { title: '整理周报', source: 'dingtalk', status: 'task' }),
      ]),
    );

    render(<InboxScreen />);

    const now = await screen.findByRole('region', { name: '现在必须做' });
    const decided = screen.getByRole('region', { name: '已分诊' });

    expect(within(now).getByText('查退款率')).toBeInTheDocument();
    expect(within(now).getByText('wechat')).toBeInTheDocument();
    expect(within(now).getByText('owner')).toBeInTheDocument();
    expect(within(now).getByText('3 条')).toBeInTheDocument();
    expect(within(now).getByText('2 分钟前')).toBeInTheDocument();
    expect(within(now).getByRole('button', { name: '派成任务' })).toBeInTheDocument();
    expect(within(now).getByRole('button', { name: /忽\s*略/ })).toBeInTheDocument();
    expect(within(now).queryByText('整理周报')).toBeNull();

    expect(within(decided).getByText('整理周报')).toBeInTheDocument();
    expect(within(decided).getByText('dingtalk')).toBeInTheDocument();
    // count === 1 is not worth a badge, and a decided row has nothing to do.
    expect(within(decided).queryByText('1 条')).toBeNull();
    expect(within(decided).queryByRole('button', { name: '派成任务' })).toBeNull();
    expect(within(decided).queryByRole('button', { name: /忽\s*略/ })).toBeNull();
  });

  it('dispatches the clicked item and re-reads the queue afterwards', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1', { title: '查退款率' })]));

    render(<InboxScreen />);

    await user.click(await screen.findByRole('button', { name: '派成任务' }));

    await waitFor(() =>
      expect(api.dispatchInboxItem).toHaveBeenCalledWith({
        id: 'inbox-1',
        subject: '查退款率',
      }),
    );
    // One read on mount, exactly one more after the write.
    await waitFor(() => expect(api.loadInboxSnapshot).toHaveBeenCalledTimes(2));
    expect(api.triageInboxItem).not.toHaveBeenCalled();
  });

  it('offers a role picker per row and sends the chosen role with the dispatch', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1', { title: '查退款率' })]));

    render(<InboxScreen />);

    // Without a way to name who takes the task, `inbox.dispatch` could only ever
    // send {id, subject}: the gateway then records an owner and wakes nobody,
    // because only the lead role starts a run.
    const picker = await screen.findByTestId('inbox-assignee-inbox-1');
    // Reachable by name too, with the word the task detail uses for the owner:
    // a picker only a mouse can find is not a control.
    expect(screen.getByRole('combobox', { name: '谁在做' })).toBeInTheDocument();
    fireEvent.mouseDown(picker.querySelector('.ant-select-selector') ?? picker);
    const leadOption = document.querySelector('.ant-select-item-option[title="编排者"]');
    expect(leadOption).not.toBeNull();
    await act(async () => {
      fireEvent.click(leadOption as Element);
    });

    await user.click(screen.getByRole('button', { name: '派成任务' }));

    await waitFor(() =>
      expect(api.dispatchInboxItem).toHaveBeenCalledWith({
        id: 'inbox-1',
        subject: '查退款率',
        assignee: 'lead',
      }),
    );
  });

  it('says why a dispatch woke nobody instead of only refreshing the list', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1', { title: '查退款率' })]));
    // The gateway created the task but skipped the wake-up (a policy decision):
    // the task exists, nobody is working on it, and the client must say so.
    api.dispatchInboxItem.mockResolvedValue({
      taskIds: ['task-9'],
      created: true,
      dispatch: {
        taskId: 'task-9',
        skipped: '任务预算已耗尽（200000 tokens / 30 分钟上限），不唤醒；需要先追加预算',
      },
    });

    render(<InboxScreen />);

    await user.click(await screen.findByRole('button', { name: '派成任务' }));

    const notice = await screen.findByRole('alert');
    expect(notice).toHaveTextContent('任务预算已耗尽');
    // The queue is still re-read: the task really was created.
    await waitFor(() => expect(api.loadInboxSnapshot).toHaveBeenCalledTimes(2));
    expect(screen.getByText('查退款率')).toBeInTheDocument();
  });

  it('reports a failed wake-up as an error, not as a quiet success', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1', { title: '查退款率' })]));
    api.dispatchInboxItem.mockResolvedValue({
      taskIds: ['task-9'],
      created: true,
      dispatch: {
        taskId: 'task-9',
        error: 'start run: no model configured',
      },
    });

    render(<InboxScreen />);

    await user.click(await screen.findByRole('button', { name: '派成任务' }));

    const notice = await screen.findByRole('alert');
    expect(notice).toHaveTextContent('no model configured');
    await waitFor(() => expect(api.loadInboxSnapshot).toHaveBeenCalledTimes(2));
  });

  it('triages 忽略 without creating a task', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1')]));

    render(<InboxScreen />);

    await user.click(await screen.findByRole('button', { name: /忽\s*略/ }));

    await waitFor(() =>
      expect(api.triageInboxItem).toHaveBeenCalledWith({ id: 'inbox-1', action: 'ignored' }),
    );
    expect(api.dispatchInboxItem).not.toHaveBeenCalled();
    await waitFor(() => expect(api.loadInboxSnapshot).toHaveBeenCalledTimes(2));
  });

  it('disables the row while its write is in flight', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1')]));
    let release: ((value: unknown) => void) | undefined;
    api.dispatchInboxItem.mockReturnValue(
      new Promise((resolve) => {
        release = resolve;
      }),
    );

    render(<InboxScreen />);

    const button = await screen.findByRole('button', { name: '派成任务' });
    await user.click(button);

    await waitFor(() => expect(screen.getByRole('button', { name: '派成任务' })).toBeDisabled());
    expect(screen.getByRole('button', { name: /忽\s*略/ })).toBeDisabled();

    release?.({ taskIds: ['task-9'], created: true });
    await waitFor(() => expect(screen.getByRole('button', { name: '派成任务' })).toBeEnabled());
  });

  it('shows the failure and a 重试 that reads again', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot
      .mockRejectedValueOnce(new Error('not_ready'))
      .mockResolvedValueOnce(snapshot([row('inbox-1', { title: '查退款率' })]));

    render(<InboxScreen />);

    expect(await screen.findByText('not_ready')).toBeInTheDocument();
    const retry = screen.getByRole('button', { name: /重\s*试/ });
    await user.click(retry);

    expect(await screen.findByText('查退款率')).toBeInTheDocument();
    expect(api.loadInboxSnapshot).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('button', { name: /重\s*试/ })).toBeNull();
  });

  it('keeps the list and reports a failed write', async () => {
    const user = userEvent.setup();
    api.loadInboxSnapshot.mockResolvedValue(snapshot([row('inbox-1', { title: '查退款率' })]));
    api.dispatchInboxItem.mockRejectedValue(new Error('inbox: unknown item'));

    render(<InboxScreen />);

    await user.click(await screen.findByRole('button', { name: '派成任务' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('inbox: unknown item');
    // The queue itself is still on screen: the write failed, nothing vanished.
    expect(screen.getByText('查退款率')).toBeInTheDocument();
    expect(api.loadInboxSnapshot).toHaveBeenCalledTimes(1);
  });
});

/**
 * Screen 05 of the interaction prototype: a phone opens this same screen over
 * the LAN at ~390px, so a long subject must not produce a sideways scrollbar and
 * the row's one action must stay hittable. The width is installed through
 * `@/test/viewport` — `window.innerWidth` plus a `matchMedia` that answers
 * `(max-width: 768px)`, which is the branch `useMediaQuery` reads.
 */
describe('InboxScreen at 390px', () => {
  // 160 characters with no space: an inbox subject that is a URL or an id.
  const LONG_TITLE = `https://example.test/refund/${'a'.repeat(140)}`;

  beforeEach(() => {
    installViewport(PHONE_WIDTH);
  });

  it('does not widen the screen, wraps the subject, and grows the row action', async () => {
    api.loadInboxSnapshot.mockResolvedValue(
      snapshot([row('inbox-1', { title: LONG_TITLE, sender: 'owner' })]),
    );

    render(<InboxScreen />);

    const subject = await screen.findByText(LONG_TITLE);
    const host = document.querySelector('.zk-screen');
    expect(host).not.toBeNull();

    // No horizontal scroll on the container that owns the scrollbars.
    expectNoHorizontalOverflow(host as Element);

    // The subject is the thing that used to widen the row: it must wrap (or
    // ellipsise) rather than push the container out.
    expectWrapsOrEllipsis(subject);
    expectWrapsOrEllipsis(document.querySelector('.zk-inbox__title') as Element, 'the row title');

    // 派成任务 is the row's primary action; antd's `small` is 24px, below a thumb.
    const dispatch = screen.getByRole('button', { name: '派成任务' });
    expect(dispatch).toBeVisible();
    expect(dispatch).toBeEnabled();
    expectTapTarget(dispatch);

    // Both verbs are reachable in the same row, so neither was pushed off-screen.
    expect(screen.getByRole('button', { name: /忽\s*略/ })).toBeVisible();
  });

  it('stacks the row actions under the subject instead of squeezing them', async () => {
    api.loadInboxSnapshot.mockResolvedValue(
      snapshot([
        row('inbox-1', {
          title: LONG_TITLE,
          sender: 'owner',
          receivedAt: new Date(Date.now() - 10 * 60_000).toISOString(),
        }),
      ]),
    );

    render(<InboxScreen />);

    await screen.findByText(LONG_TITLE);
    const rowElement = document.querySelector('.zk-inbox__row') as HTMLElement;
    // Column direction at 390: the title owns the full width and the actions sit
    // below it, which is what keeps a long subject from stealing their room.
    expect(getComputedStyle(rowElement).flexDirection).toBe('column');

    // Sender and stamp are two facts, so they need a gap: without one they read
    // as the single token "owner10 分钟前" (Chromium pass at 390px).
    const meta = document.querySelector('.zk-inbox__meta') as HTMLElement;
    expect(getComputedStyle(meta).display).toBe('flex');
    expect(Number.parseFloat(getComputedStyle(meta).gap || '0')).toBeGreaterThan(0);
  });
});