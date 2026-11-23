import { describe, expect, it } from 'vitest';
import { defaultState, installFakeGateway } from '@/test/fake-gateway';

/**
 * task-38 C: `session.active.get` reports the **current** selection — a constant
 * answer would pass a naive "it replied" test and fail the product.
 */
describe('session.active.get', () => {
  it('follows the active session instead of answering a constant', () => {
    const state = defaultState();
    state.activeSessionId = 's-1';
    const handle = installFakeGateway(state);

    const ask = () => {
      const replies: Array<Record<string, unknown>> = [];
      const socket = {
        push: (frame: Record<string, unknown>) => replies.push(frame),
        closed: false,
      };
      handle.gateway.receive(socket as never, {
        jsonrpc: '2.0',
        id: 1,
        method: 'session.active.get',
        params: {},
      });
      return (replies.at(-1)?.result as { sessionId?: string | null } | undefined)?.sessionId;
    };

    expect(ask()).toBe('s-1');
    state.activeSessionId = 's-2';
    expect(ask()).toBe('s-2');
    state.activeSessionId = null;
    expect(ask()).toBeNull();
    handle.dispose();
  });
});
