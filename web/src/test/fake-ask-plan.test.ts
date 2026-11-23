import { describe, expect, it } from 'vitest';
import { defaultState, installFakeGateway } from '@/test/fake-gateway';

/**
 * task-38 B: `ask.answer` / `ask.cancel` / `plan.exit` are state changes, so the
 * assertion is on the state (and on the delta reaching the socket), never on
 * "the command returned {}".
 */
describe('ask / plan commands change state', () => {
  it('ask.answer stores the answer and pushes ask.delta', () => {
    const state = defaultState();
    state.ask = { id: 'ask-1', text: '选哪个？' };
    const handle = installFakeGateway(state);
    const frames: Array<Record<string, unknown>> = [];
    handle.gateway.sockets.push({
      push: (frame: Record<string, unknown>) => frames.push(frame),
      closed: false,
    } as never);
    handle.gateway.receive(handle.gateway.openTestSocket(), {
      jsonrpc: '2.0',
      id: 1,
      method: 'ask.answer',
      params: { id: 'ask-1', answer: 'A' },
    });
    expect(state.ask?.answer).toBe('A');
    expect(frames.some((frame) => frame.method === 'ask.delta')).toBe(true);
    handle.dispose();
  });

  it('ask.cancel clears the pending question and pushes ask.delta', () => {
    const state = defaultState();
    state.ask = { id: 'ask-1', text: '选哪个？' };
    const handle = installFakeGateway(state);
    const frames: Array<Record<string, unknown>> = [];
    handle.gateway.sockets.push({
      push: (frame: Record<string, unknown>) => frames.push(frame),
      closed: false,
    } as never);
    handle.gateway.receive(handle.gateway.openTestSocket(), {
      jsonrpc: '2.0',
      id: 2,
      method: 'ask.cancel',
      params: { id: 'ask-1' },
    });
    expect(state.ask).toBeNull();
    expect(frames.some((frame) => frame.method === 'ask.delta')).toBe(true);
    handle.dispose();
  });

  it('plan.exit moves the plan state and pushes plan.delta', () => {
    const state = defaultState();
    const handle = installFakeGateway(state);
    const frames: Array<Record<string, unknown>> = [];
    handle.gateway.sockets.push({
      push: (frame: Record<string, unknown>) => frames.push(frame),
      closed: false,
    } as never);
    handle.gateway.receive(handle.gateway.openTestSocket(), {
      jsonrpc: '2.0',
      id: 3,
      method: 'plan.exit',
      params: { action: 'exit' },
    });
    expect(state.plan.state).toBe('exit');
    expect(frames.some((frame) => frame.method === 'plan.delta')).toBe(true);
    handle.dispose();
  });
});
