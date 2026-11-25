import { beforeEach, describe, expect, it, vi } from 'vitest';
import { actAsUser, loadUsers, upsertUser, USER_ROLES } from '@/api/users';
import { roleAllows, USER_ACTIONS } from './roles';

/**
 * The gateway boundary for the roster (M6), pinned: which method name and which
 * parameters, the two things a client can get wrong silently. The transport
 * itself (frames, reconnect, the auth gate) is `api/ws/rpc.ts`'s own suite —
 * same split as `features/task/task-api.test.ts`.
 *
 * The role→action matrix is asserted here too, because the screen renders it as
 * a legend: a wrong row would tell a person they may approve something the
 * gateway then refuses.
 */
const wire = vi.hoisted(() => ({ request: vi.fn() }));

vi.mock('@/api/gateway', () => ({ authorizationReady: () => Promise.resolve(true) }));
vi.mock('@/api/ws/rpc', () => ({ gatewayRpc: () => wire }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('users api', () => {
  it('reads the roster with no parameters and keeps the acting identity', async () => {
    wire.request.mockResolvedValue({
      users: [
        { id: 'owner-1', name: 'Owner', role: 'owner', createdAt: '2026-11-01T00:00:00Z' },
        { id: 'alice', name: 'Alice', role: 'manager' },
      ],
      actingUserId: 'alice',
      actingRole: 'manager',
    });

    const snapshot = await loadUsers();

    expect(wire.request).toHaveBeenCalledWith('users.snapshot', {});
    expect(snapshot.users).toEqual([
      { id: 'owner-1', name: 'Owner', role: 'owner', createdAt: '2026-11-01T00:00:00Z' },
      { id: 'alice', name: 'Alice', role: 'manager' },
    ]);
    expect(snapshot.actingUserId).toBe('alice');
    expect(snapshot.actingRole).toBe('manager');
  });

  it('survives an empty answer instead of returning undefined', async () => {
    wire.request.mockResolvedValue({});

    await expect(loadUsers()).resolves.toEqual({ users: [] });
  });

  it('keeps a role this build does not know verbatim, and omits an absent acting pair', async () => {
    wire.request.mockResolvedValue({ users: [{ id: 'x1', name: 'X', role: 'auditor' }] });

    const snapshot = await loadUsers();

    expect(snapshot.users[0].role).toBe('auditor');
    expect(snapshot.actingUserId).toBeUndefined();
    expect(snapshot.actingRole).toBeUndefined();
  });

  it('sends exactly {id,name,role} to users.upsert', async () => {
    wire.request.mockResolvedValue({ id: 'carol', name: 'Carol', role: 'collaborator' });

    const stored = await upsertUser({ id: 'carol', name: 'Carol', role: 'collaborator' });

    // The generated contract requires all three (`WS_PARAMS['users.upsert']`):
    // there is no patch shape for a member, so a change is always the whole row.
    expect(wire.request).toHaveBeenCalledWith('users.upsert', {
      id: 'carol',
      name: 'Carol',
      role: 'collaborator',
    });
    expect(stored).toEqual({ id: 'carol', name: 'Carol', role: 'collaborator' });
  });

  it('trims the form values it sends', async () => {
    wire.request.mockResolvedValue({});

    await upsertUser({ id: ' carol ', name: ' Carol ', role: ' manager ' });

    expect(wire.request).toHaveBeenCalledWith('users.upsert', {
      id: 'carol',
      name: 'Carol',
      role: 'manager',
    });
  });

  it('sends exactly {id} to users.act and reports the acting id it now holds', async () => {
    wire.request.mockResolvedValue({ actingUserId: 'alice' });

    await expect(actAsUser('alice')).resolves.toBe('alice');
    // One key, no role, no name: the switch says *who*, and the gateway keeps
    // the rest of the record.
    expect(wire.request).toHaveBeenCalledWith('users.act', { id: 'alice' });
  });

  it('falls back to the requested id when the gateway echoes nothing', async () => {
    wire.request.mockResolvedValue({});

    await expect(actAsUser('bob')).resolves.toBe('bob');
  });
});

describe('the role policy the legend shows', () => {
  it('lists the three roles in decreasing power', () => {
    expect([...USER_ROLES]).toEqual(['owner', 'manager', 'collaborator']);
  });

  it('matches contracts.Allowed exactly — five actions, owner first', () => {
    const allowed = (role: string) => USER_ACTIONS.filter((action) => roleAllows(role, action));
    expect([...USER_ACTIONS]).toEqual([
      'dispatch',
      'approve',
      'manage-bots',
      'review-memory',
      'decide-budget',
    ]);
    // owner: everything. manager: everything except the roster of bots, which is
    // the one action that could grant itself the rest.
    expect(allowed('owner')).toEqual([...USER_ACTIONS]);
    expect(allowed('manager')).toEqual([
      'dispatch',
      'approve',
      'review-memory',
      'decide-budget',
    ]);
    // collaborator: dispatch only.
    expect(allowed('collaborator')).toEqual(['dispatch']);
  });

  it('denies by default — an unknown role and an unknown action get nothing', () => {
    expect(roleAllows('auditor', 'dispatch')).toBe(false);
    expect(roleAllows('', 'approve')).toBe(false);
    expect(roleAllows('owner', 'delete-everything' as never)).toBe(false);
  });
});