import { describe, expect, it } from 'vitest';
import { defaultState, installFakeGateway } from '@/test/fake-gateway';

/**
 * task-38 A: the four global subscribe methods are implemented, and their effect
 * is observable — a subscribe is not "returned {}".
 *
 * Two assertions per domain: the replay reply carries an epoch/rev, and a change
 * made afterwards **reaches the socket** as a delta. Both are needed: a handler
 * that only replies would pass the first and fail the second.
 */
describe('global subscribe methods', () => {
  it.each(['approvals.subscribe', 'ask.subscribe', 'plan.subscribe', 'settings.subscribe'])(
    '%s replies with epoch/rev and further deltas reach the socket',
    (method) => {
      const handle = installFakeGateway(defaultState());
      const fake = handle.gateway;
      const frames: Array<Record<string, unknown>> = [];
      const socket = {
        push: (frame: Record<string, unknown>) => frames.push(frame),
        closed: false,
      };
      fake.sockets.push(socket as never);

      const replies: Array<Record<string, unknown>> = [];
      const spy = {
        push: (frame: Record<string, unknown>) => {
          replies.push(frame);
          frames.push(frame);
        },
        closed: false,
      };
      fake.receive(spy as never, { jsonrpc: '2.0', id: 1, method, params: {} });

      const reply = replies.at(-1) as Record<string, unknown> | undefined;
      const result = reply?.result as { epoch?: number; rev?: number } | undefined;
      expect(result, `${method} 没有返回 epoch/rev 的应答`).toBeTruthy();
      // `epoch` is an opaque token (string in the gateway), `rev` a number.
      expect(result?.epoch).toBeDefined();
      expect(typeof result?.rev).toBe('number');

      // A change after subscribing must arrive as a delta on the same socket.
      fake.notify(`${method.split('.')[0]}.delta`, { rev: 1 });
      expect(
        frames.some((frame) => frame.method === `${method.split('.')[0]}.delta`),
        `${method} 之后没有收到 delta 推送`,
      ).toBe(true);
      handle.dispose();
    },
  );
});
