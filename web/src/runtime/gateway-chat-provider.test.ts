import { describe, expect, it, vi } from 'vitest';
import type { SSEOutput } from '@ant-design/x-sdk';
import {
  GatewayChatProvider,
  applyChunk,
  newClientRunId,
} from './gateway-chat-provider';
import { assistantMessage, toolSummary } from '@/chat/types';

const chunk = (event: string, data: unknown): SSEOutput => ({
  event,
  data: typeof data === 'string' ? data : JSON.stringify(data),
});

const fold = (chunks: SSEOutput[]) =>
  chunks.reduce((message, item) => applyChunk(message, item), assistantMessage());

describe('GatewayChatProvider', () => {
  it('turns onRequest params into the POST body it was given', () => {
    const provider = new GatewayChatProvider();
    const body = provider.transformParams(
      { sessionId: 's-1', text: 'hello', clientRunId: 'run-1' },
      { params: { sessionId: 's-1' } } as never,
    );
    expect(body).toEqual({ sessionId: 's-1', text: 'hello', clientRunId: 'run-1' });
  });

  it('mints a clientRunId when the caller does not supply one', () => {
    const provider = new GatewayChatProvider();
    const body = provider.transformParams({ sessionId: 's-1', text: 'hi' }, {} as never);
    expect(body.clientRunId).toMatch(/\S/);
    expect(newClientRunId()).not.toBe(newClientRunId());
  });

  it('refuses to run without a session or text', () => {
    const provider = new GatewayChatProvider();
    expect(() => provider.transformParams({ text: 'hi' }, {} as never)).toThrow(/sessionId/);
    expect(() => provider.transformParams({ sessionId: 's-1' }, {} as never)).toThrow(/text/);
  });

  it('shows the user message immediately', () => {
    const provider = new GatewayChatProvider();
    expect(provider.transformLocalMessage({ text: 'hi' })).toMatchObject({
      role: 'user',
      text: 'hi',
    });
  });

  it('folds the seven documented events into one assistant message', () => {
    const message = fold([
      chunk('reasoning.delta', { turnId: 't1', text: '先想一想' }),
      chunk('tool.start', { callId: 'c1', name: 'read_file', input: { path: 'a.ts' } }),
      chunk('tool.result', { callId: 'c1', ok: true, output: 'contents' }),
      chunk('message.delta', { turnId: 't1', text: '答案是' }),
      chunk('message.delta', { turnId: 't1', text: '42。' }),
      chunk('usage', { inputTokens: 10, outputTokens: 5 }),
      chunk('done', { turnId: 't1', stopReason: 'end_turn' }),
    ]);

    expect(message.text).toBe('答案是42。');
    expect(message.reasoning).toBe('先想一想');
    expect(message.steps).toHaveLength(1);
    expect(message.steps[0]).toMatchObject({ callId: 'c1', name: 'read_file', status: 'ok' });
    expect(message.usage).toEqual({ inputTokens: 10, outputTokens: 5 });
    expect(message.stopReason).toBe('end_turn');
    expect(message.error).toBeUndefined();
  });

  it('marks a failed tool and keeps its error text', () => {
    const message = fold([
      chunk('tool.start', { callId: 'c1', name: 'run_command', input: { command: 'go test' } }),
      chunk('tool.result', { callId: 'c1', ok: false, error: 'exit status 1' }),
    ]);
    expect(message.steps[0]).toMatchObject({ status: 'error', error: 'exit status 1' });
  });

  it('keeps a tool result whose start was never seen instead of dropping it', () => {
    const message = fold([chunk('tool.result', { callId: 'c9', ok: true, output: 'x' })]);
    expect(message.steps).toHaveLength(1);
    expect(message.steps[0]?.status).toBe('ok');
  });

  it('records a terminal error event', () => {
    const message = fold([
      chunk('error', { code: 'provider_unavailable', message: '模型服务暂时不可用' }),
    ]);
    expect(message.error).toEqual({
      code: 'provider_unavailable',
      message: '模型服务暂时不可用',
    });
  });

  it('degrades an unknown event instead of throwing (C3)', () => {
    const message = fold([chunk('telemetry.ping', { at: 1 })]);
    expect(message.text).toBe('');
    expect(message.unknownEvents).toEqual(['telemetry.ping']);
  });

  it('degrades malformed JSON data instead of throwing', () => {
    const message = fold([{ event: 'message.delta', data: '{not json' }]);
    expect(message.unknownEvents).toEqual(['message.delta']);
  });

  it('ignores a chunk with no event name', () => {
    const message = fold([{ data: '{}' }]);
    expect(message.unknownEvents).toBeUndefined();
    expect(message.text).toBe('');
  });

  it('rebuilds from all chunks when the stream ended without any update', () => {
    const provider = new GatewayChatProvider();
    const message = provider.transformMessage({
      chunk: undefined as unknown as SSEOutput,
      chunks: [
        chunk('message.delta', { turnId: 't1', text: 'only ' }),
        chunk('message.delta', { turnId: 't1', text: 'chunks' }),
      ],
      status: 'success',
      responseHeaders: new Headers(),
    });
    expect(message.text).toBe('only chunks');
  });

  it('passes the live message through on the final success call', () => {
    const provider = new GatewayChatProvider();
    const origin = { ...assistantMessage(), text: 'streamed' };
    const message = provider.transformMessage({
      originMessage: origin,
      chunk: undefined as unknown as SSEOutput,
      chunks: [],
      status: 'success',
      responseHeaders: new Headers(),
    });
    expect(message.text).toBe('streamed');
  });

  it('does not replay every chunk onto a row that already holds them', () => {
    // Regression: onSuccess hands over the full chunk list plus the live row.
    // Folding the list again duplicated the whole answer in the transcript.
    const provider = new GatewayChatProvider();
    const chunks = [
      chunk('message.delta', { turnId: 't1', text: '答案' }),
      chunk('done', { turnId: 't1', stopReason: 'end_turn' }),
    ];
    const live = chunks.reduce(
      (accumulated, item) => applyChunk(accumulated, item),
      assistantMessage(),
    );
    const final = provider.transformMessage({
      originMessage: live,
      chunk: undefined as unknown as SSEOutput,
      chunks,
      status: 'success',
      responseHeaders: new Headers(),
    });
    expect(final.text).toBe('答案');
    expect(final.stopReason).toBe('end_turn');
  });
});

describe('toolSummary', () => {
  it('uses the most meaningful input field', () => {
    expect(toolSummary({ path: 'web/src/app.tsx', offset: 1 })).toBe('web/src/app.tsx');
    expect(toolSummary({ command: 'go test ./...' })).toBe('go test ./...');
    expect(toolSummary('  plain   text  ')).toBe('plain text');
    expect(toolSummary(undefined)).toBe('');
  });

  it('flattens long input to one clipped line', () => {
    const summary = toolSummary({ query: 'x'.repeat(400) });
    expect(summary.length).toBeLessThanOrEqual(120);
    expect(summary.endsWith('…')).toBe(true);
  });
});

describe('transformMessage wiring', () => {
  it('never mutates the previous message object', () => {
    const provider = new GatewayChatProvider();
    const origin = { ...assistantMessage(), text: 'a' };
    const next = provider.transformMessage({
      originMessage: origin,
      chunk: chunk('message.delta', { turnId: 't', text: 'b' }),
      chunks: [],
      status: 'updating',
      responseHeaders: new Headers(),
    });
    expect(origin.text).toBe('a');
    expect(next).not.toBe(origin);
    expect(next.text).toBe('ab');
  });

  it('is what the provider hands to x-sdk for each update', () => {
    const provider = new GatewayChatProvider();
    const onUpdate = vi.fn();
    provider.injectRequest({ onUpdate, onSuccess: vi.fn(), onError: vi.fn() });
    // The callbacks are owned by useXChat; this asserts the provider exposes
    // exactly the three abstract transforms plus the injected request seam.
    expect(typeof provider.transformParams).toBe('function');
    expect(typeof provider.transformLocalMessage).toBe('function');
    expect(typeof provider.transformMessage).toBe('function');
    expect(onUpdate).not.toHaveBeenCalled();
  });
});
/**
 * A `tool.result` whose `tool.start` never reached the client (a replayed run, or a
 * result for a step the transcript had not created yet) used to name the step with
 * the raw call id, so the reader saw `call_93bca2c6ab4b478f9fa84247` where a tool
 * name belongs. The name comes from the frame; when it is absent, the step stays
 * nameless and the card falls back to its generic label.
 */
describe('tool.result without a start', () => {
  it('uses the frame name when present', () => {
    const after = applyChunk(
      assistantMessage(),
      chunk('tool.result', { callId: 'call-9', name: 'bash', ok: true, output: 'ok' }),
    );
    expect(after.steps.at(-1)?.name).toBe('bash');
  });

  it('never uses the call id as the tool name', () => {
    const after = applyChunk(
      assistantMessage(),
      chunk('tool.result', { callId: 'call-9', ok: true, output: 'ok' }),
    );
    const step = after.steps.at(-1);
    expect(step?.callId).toBe('call-9');
    expect(step?.name).not.toContain('call-9');
  });
});
