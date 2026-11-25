import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ALL_INBOX_STATUSES,
  dispatchInboxItem,
  loadInboxSnapshot,
  triageInboxItem,
} from '@/api/inbox';

/**
 * The gateway boundary, pinned: which method name, which parameters — the two
 * things a client can get wrong silently. The transport itself (frames,
 * reconnect, the auth gate) is `api/ws/rpc.ts`'s own suite.
 */
const wire = vi.hoisted(() => ({ request: vi.fn() }));

vi.mock('@/api/gateway', () => ({ authorizationReady: () => Promise.resolve(true) }));
vi.mock('@/api/ws/rpc', () => ({ gatewayRpc: () => wire }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('inbox api', () => {
  it('reads the queue with the statuses the screen needs on the wire', async () => {
    wire.request.mockResolvedValue({
      items: [{ id: 'inbox-1', source: 'wechat', title: '查退款率', status: 'new', count: 2 }],
      stats: { total: 1, new: 1, triaged: 0 },
    });

    const snapshot = await loadInboxSnapshot({ statuses: ALL_INBOX_STATUSES });

    expect(wire.request).toHaveBeenCalledWith('inbox.snapshot', {
      statuses: ['new', 'triaged', 'task', 'ignored', 'archived'],
    });
    expect(snapshot.items).toHaveLength(1);
    expect(snapshot.stats).toEqual({ total: 1, new: 1, triaged: 0 });
  });

  it('sends no filter fields when there is no filter', async () => {
    wire.request.mockResolvedValue({ items: [], stats: { total: 0, new: 0, triaged: 0 } });

    await loadInboxSnapshot();

    // The default filter is the gateway's ("still actionable"); the client does
    // not restate it with an empty array.
    expect(wire.request).toHaveBeenCalledWith('inbox.snapshot', {});
  });

  it('survives an answer with no items or stats instead of rendering NaN', async () => {
    wire.request.mockResolvedValue({});

    await expect(loadInboxSnapshot()).resolves.toEqual({
      items: [],
      stats: { total: 0, new: 0, triaged: 0 },
    });
  });

  it('dispatches with {id, subject} and only adds assignee when asked', async () => {
    wire.request.mockResolvedValue({ taskIds: ['task-9'], created: true });

    await dispatchInboxItem({ id: 'inbox-1', subject: '查退款率' });
    expect(wire.request).toHaveBeenLastCalledWith('inbox.dispatch', {
      id: 'inbox-1',
      subject: '查退款率',
    });

    await dispatchInboxItem({ id: 'inbox-2', subject: '整理周报', assignee: 'bot-1' });
    expect(wire.request).toHaveBeenLastCalledWith('inbox.dispatch', {
      id: 'inbox-2',
      subject: '整理周报',
      assignee: 'bot-1',
    });
  });

  it('triages with {id, action}', async () => {
    wire.request.mockResolvedValue({
      id: 'inbox-1',
      source: 'wechat',
      title: '查退款率',
      status: 'ignored',
      count: 1,
    });

    const item = await triageInboxItem({ id: 'inbox-1', action: 'ignored' });

    expect(wire.request).toHaveBeenCalledWith('inbox.triage', {
      id: 'inbox-1',
      action: 'ignored',
    });
    expect(item.status).toBe('ignored');
  });
});