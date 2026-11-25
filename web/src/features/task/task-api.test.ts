import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  claimTask,
  heartbeatTask,
  loadTaskBoard,
  loadTaskLease,
  reviewTask,
  submitTask,
} from '@/api/task';

/**
 * The gateway boundary, pinned: which method name and which parameters, the two
 * things a client can get wrong silently. The transport itself (frames,
 * reconnect, the auth gate) is `api/ws/rpc.ts`'s own suite — same split as
 * `features/inbox/inbox-api.test.ts`.
 */
const wire = vi.hoisted(() => ({ request: vi.fn() }));

vi.mock('@/api/gateway', () => ({ authorizationReady: () => Promise.resolve(true) }));
vi.mock('@/api/ws/rpc', () => ({ gatewayRpc: () => wire }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('task api', () => {
  it('reads the process-wide board with no sessionId on the wire', async () => {
    wire.request.mockResolvedValue({
      epoch: 'e1',
      rev: 3,
      tasks: [{ id: 'task-1', subject: '查退款率', status: 'pending' }],
    });

    const board = await loadTaskBoard();

    expect(wire.request).toHaveBeenCalledWith('tasks.snapshot', {});
    expect(board).toEqual([{ id: 'task-1', subject: '查退款率', status: 'pending' }]);
  });

  it('passes sessionId through when a caller scopes the board to one session', async () => {
    wire.request.mockResolvedValue({ tasks: [] });

    await loadTaskBoard('s-1');

    expect(wire.request).toHaveBeenCalledWith('tasks.snapshot', { sessionId: 's-1' });
  });

  it('survives a board answer with no rows instead of returning undefined', async () => {
    wire.request.mockResolvedValue({});

    await expect(loadTaskBoard()).resolves.toEqual([]);
  });

  it('keeps an unknown status instead of inventing one, and drops a nameless id-less row', async () => {
    wire.request.mockResolvedValue({
      tasks: [
        { id: 'task-2', status: 'paused_by_user', owner: 'bot-1' },
        { subject: '没有 id 的行' },
      ],
    });

    const board = await loadTaskBoard();

    // The id doubles as the subject so the row stays addressable; the status is
    // the protocol value — the screen owns the human word for it.
    expect(board).toEqual([
      { id: 'task-2', subject: 'task-2', status: 'paused_by_user', owner: 'bot-1' },
    ]);
  });

  it('reads the lease with {id} and nothing else', async () => {
    wire.request.mockResolvedValue({
      taskId: 'task-1',
      owner: 'bot-1',
      state: 'review',
      evidence: [{ kind: 'link', ref: 'https://example.com/r' }],
    });

    const lease = await loadTaskLease({ id: 'task-1' });

    // docs/TRANSPORT.md §5.1.13 documents the read as read-only: {id}.
    expect(wire.request).toHaveBeenCalledWith('tasks.lease', { id: 'task-1' });
    expect(lease?.state).toBe('review');
    expect(lease?.evidence).toEqual([{ kind: 'link', ref: 'https://example.com/r' }]);
  });

  it('answers null for {lease:null}, which is "no claim" and not a failure', async () => {
    wire.request.mockResolvedValue({ lease: null });

    await expect(loadTaskLease({ id: 'task-1' })).resolves.toBeNull();
  });

  it('unwraps a {lease:{...}} envelope and falls back to the requested id', async () => {
    wire.request.mockResolvedValue({ lease: { owner: 'bot-1', state: 'claimed' } });

    const lease = await loadTaskLease({ id: 'task-1' });

    expect(lease).toEqual({ taskId: 'task-1', owner: 'bot-1', state: 'claimed' });
  });

  it('claims with exactly {id, owner} and returns the lease', async () => {
    wire.request.mockResolvedValue({
      taskId: 'task-1',
      owner: 'lead',
      state: 'claimed',
      until: '2026-11-25T09:10:00Z',
      heartbeat: '2026-11-25T09:00:00Z',
    });

    const lease = await claimTask({ id: 'task-1', owner: 'lead' });

    expect(wire.request).toHaveBeenCalledWith('tasks.claim', { id: 'task-1', owner: 'lead' });
    expect(lease.state).toBe('claimed');
    expect(lease.until).toBe('2026-11-25T09:10:00Z');
  });

  it('heartbeats with exactly {id, owner}', async () => {
    wire.request.mockResolvedValue({ taskId: 'task-1', owner: 'lead', state: 'claimed' });

    await heartbeatTask({ id: 'task-1', owner: 'lead' });

    expect(wire.request).toHaveBeenCalledWith('tasks.heartbeat', {
      id: 'task-1',
      owner: 'lead',
    });
  });

  it('submits evidence as [{kind, ref, note?}] and keeps note only when given', async () => {
    wire.request.mockResolvedValue({
      taskId: 'task-1',
      owner: 'lead',
      state: 'review',
      evidence: [
        { kind: 'link', ref: 'https://example.com/report', note: '报告' },
        { kind: 'file', ref: 'out/report.md' },
      ],
    });

    const lease = await submitTask({
      id: 'task-1',
      owner: 'lead',
      evidence: [
        { kind: 'link', ref: 'https://example.com/report', note: '报告' },
        { kind: 'file', ref: 'out/report.md' },
      ],
    });

    expect(wire.request).toHaveBeenCalledWith('tasks.submit', {
      id: 'task-1',
      owner: 'lead',
      evidence: [
        { kind: 'link', ref: 'https://example.com/report', note: '报告' },
        { kind: 'file', ref: 'out/report.md' },
      ],
    });
    expect(lease.state).toBe('review');
    expect(lease.evidence).toHaveLength(2);
  });

  it('accepts with {id, decision, reviewer} and no note field', async () => {
    wire.request.mockResolvedValue({ taskId: 'task-1', owner: 'lead', state: 'done' });

    const lease = await reviewTask({ id: 'task-1', decision: 'accept', reviewer: 'lead' });

    expect(wire.request).toHaveBeenCalledWith('tasks.review', {
      id: 'task-1',
      decision: 'accept',
      reviewer: 'lead',
    });
    expect(lease.state).toBe('done');
  });

  it('rejects with the reason as the note, and carries the reviewer note back', async () => {
    wire.request.mockResolvedValue({
      taskId: 'task-1',
      owner: 'lead',
      state: 'rejected',
      reviewNote: '少了第三家',
    });

    const lease = await reviewTask({
      id: 'task-1',
      decision: 'reject',
      reviewer: 'lead',
      note: '少了第三家',
    });

    expect(wire.request).toHaveBeenCalledWith('tasks.review', {
      id: 'task-1',
      decision: 'reject',
      reviewer: 'lead',
      note: '少了第三家',
    });
    expect(lease.reviewNote).toBe('少了第三家');
  });

  it('falls back to the request identity when the lease omits it', async () => {
    wire.request.mockResolvedValue({ state: 'claimed' });

    const lease = await claimTask({ id: 'task-9', owner: 'lead' });

    expect(lease).toEqual({ taskId: 'task-9', owner: 'lead', state: 'claimed' });
  });
});