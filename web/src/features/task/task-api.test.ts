import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  claimTask,
  heartbeatTask,
  loadTaskBoard,
  loadTaskBudget,
  loadTaskLease,
  loadTaskTools,
  reviewTask,
  setTaskTools,
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

/**
 * 工具绑定 (M5 execution policy): `tasks.tools.get` / `tasks.tools.set`.
 *
 * The read's "no binding" answer is `{tools:null}` — the same absent shape as
 * `tasks.lease`'s `{lease:null}` — and it is an answer, not a failure. A clear
 * is `set` with `[]`, which the gateway stores as "no binding", so a read that
 * follows a clear answers `{tools:null}` again.
 */
describe('task tools api', () => {
  it('reads the binding with {id} and nothing else', async () => {
    wire.request.mockResolvedValue({
      taskId: 'task-1',
      tools: ['browser_open', 'mcp__github__search'],
      updatedAt: '2026-11-25T09:00:00Z',
    });

    const binding = await loadTaskTools('task-1');

    expect(wire.request).toHaveBeenCalledWith('tasks.tools.get', { id: 'task-1' });
    expect(binding).toEqual({
      taskId: 'task-1',
      tools: ['browser_open', 'mcp__github__search'],
      updatedAt: '2026-11-25T09:00:00Z',
    });
  });

  it('answers null for {tools:null}: unbound, not a failure', async () => {
    wire.request.mockResolvedValue({ tools: null });

    await expect(loadTaskTools('task-1')).resolves.toBeNull();
  });

  it('answers null when the answer carries no list at all', async () => {
    wire.request.mockResolvedValue({});

    await expect(loadTaskTools('task-1')).resolves.toBeNull();
  });

  it('falls back to the requested id and keeps blank names out of the binding', async () => {
    wire.request.mockResolvedValue({ tools: ['browser_open', '  ', ''] });

    const binding = await loadTaskTools('task-1');

    expect(binding).toEqual({ taskId: 'task-1', tools: ['browser_open'] });
  });

  it('writes the full list as {id, tools} and returns the binding', async () => {
    wire.request.mockResolvedValue({ taskId: 'task-1', tools: ['a', 'b'] });

    const binding = await setTaskTools({ id: 'task-1', tools: ['a', 'b'] });

    expect(wire.request).toHaveBeenCalledWith('tasks.tools.set', {
      id: 'task-1',
      tools: ['a', 'b'],
    });
    expect(binding).toEqual({ taskId: 'task-1', tools: ['a', 'b'] });
  });

  it('clears with an explicit empty list, never with null', async () => {
    wire.request.mockResolvedValue({ taskId: 'task-1', updatedAt: '2026-11-25T09:00:00Z' });

    const binding = await setTaskTools({ id: 'task-1', tools: [] });

    // `tools:null` would be rejected by the gateway (1010): a policy is never
    // removed by a malformed request, so a clear is an empty list.
    expect(wire.request).toHaveBeenCalledWith('tasks.tools.set', { id: 'task-1', tools: [] });
    expect(binding).toEqual({ taskId: 'task-1', updatedAt: '2026-11-25T09:00:00Z' });
  });
});

/**
 * 预算 (`tasks.budget.get`, G11): the per-task cost ceiling.
 *
 * The read has three legal answers and this layer must not blur them: a row (a
 * ceiling exists, possibly already spent), `{budget:null}` (no ceiling — an
 * answer, not a failure), and a thrown error (the read itself failed). `0` on
 * either limit means "unlimited", so the numbers are kept verbatim rather than
 * normalized away.
 */
describe('task budget api', () => {
  it('reads the budget with {id} and nothing else', async () => {
    wire.request.mockResolvedValue({
      taskId: 'task-1',
      maxTokens: 1000,
      maxMinutes: 30,
      usedTokens: 120,
      startedAt: '2026-11-25T09:00:00Z',
      exhausted: false,
    });

    const budget = await loadTaskBudget('task-1');

    expect(wire.request).toHaveBeenCalledWith('tasks.budget.get', { id: 'task-1' });
    expect(budget).toEqual({
      taskId: 'task-1',
      maxTokens: 1000,
      maxMinutes: 30,
      usedTokens: 120,
      startedAt: '2026-11-25T09:00:00Z',
      exhausted: false,
    });
  });

  it('answers null for {budget:null}: no ceiling, not a failure', async () => {
    wire.request.mockResolvedValue({ budget: null });

    await expect(loadTaskBudget('task-1')).resolves.toBeNull();
  });

  it('unwraps a {budget:{...}} envelope and falls back to the requested id', async () => {
    wire.request.mockResolvedValue({ budget: { maxTokens: 10, usedTokens: 10, exhausted: true } });

    const budget = await loadTaskBudget('task-7');

    expect(budget).toEqual({
      taskId: 'task-7',
      maxTokens: 10,
      maxMinutes: 0,
      usedTokens: 10,
      exhausted: true,
    });
  });

  it('keeps a missing limit as 0 (unlimited) and only reports a real stop', async () => {
    // A gateway that omits the limits must not make the screen show `NaN` or
    // treat "no ceiling" as "exhausted".
    wire.request.mockResolvedValue({ taskId: 'task-1', usedTokens: 5 });

    const budget = await loadTaskBudget('task-1');

    expect(budget).toEqual({ taskId: 'task-1', maxTokens: 0, maxMinutes: 0, usedTokens: 5, exhausted: false });
  });
});
