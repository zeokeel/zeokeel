import { describe, expect, it } from 'vitest';
import { defaultState, installFakeGateway } from '@/test/fake-gateway';

/**
 * task-38: `task.delete` offers an undo, `notify.action` runs it.
 *
 * The point of this test: the gateway registers the action **by toast id**, not
 * by `actionId` — every undo is called `"restore"`, so keying by actionId would
 * make the second delete overwrite the first, and "undo" would restore the wrong
 * task. The only assertion that catches that is deleting two rows and undoing the
 * first one.
 */
describe('task delete + undo', () => {
  it('deleting two tasks then undoing the first restores it and leaves the second deleted', async () => {
    const state = defaultState();
    state.tasks = { '': [{ id: 't-1', title: 'first' }, { id: 't-2', title: 'second' }] };
    const handle = installFakeGateway(state);
    const fake = handle.gateway;
    const socket = fake.openTestSocket();
    fake.receive(socket, { jsonrpc: '2.0', id: 1, method: 'task.delete', params: { id: 't-1' } });
    fake.receive(socket, { jsonrpc: '2.0', id: 2, method: 'task.delete', params: { id: 't-2' } });

    // Two deletes → two toasts, both offering the same `actionId`.
    expect(fake.toasts.map((toast) => toast.actionId)).toEqual(['restore', 'restore']);
    expect(fake.toasts).toHaveLength(2);

    // Undo the **first** toast: t-1 comes back, t-2 stays deleted.
    fake.receive(socket, {
      jsonrpc: '2.0',
      id: 3,
      method: 'notify.action',
      params: { id: fake.toasts[0].id, actionId: 'restore' },
    });

    const ids = (state.tasks[''] ?? []).map((task) => task.id);
    expect(ids, 't-1 必须恢复').toContain('t-1');
    expect(ids, 't-2 必须仍然删除').not.toContain('t-2');

    // The action is consumed: undoing the same toast again must not resurrect
    // t-2 (the closure is gone), and the board stays as it is.
    fake.receive(socket, {
      jsonrpc: '2.0',
      id: 4,
      method: 'notify.action',
      params: { id: fake.toasts[0].id, actionId: 'restore' },
    });
    expect((state.tasks[''] ?? []).map((task) => task.id)).toEqual(['t-1']);
    handle.dispose();
  });
});
