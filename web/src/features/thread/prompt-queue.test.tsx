import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';
import { App } from '@/app';

/**
 * A5/B13 — a prompt sent while the session runs waits in the session queue
 * instead of being dropped, and the queue survives an abort.
 *
 * The run transport is stubbed but real (`XRequest` over a controllable SSE
 * stream); the queue is the WS `prompts:<sessionID>` domain served by the fake
 * gateway, so the test exercises the same snapshot/delta path as production.
 */

interface Queued {
  id: string;
  sessionId: string;
  text: string;
}

interface Harness {
  queued: Queued[];
  requested: string[];
  /** One controller per in-flight run, oldest first. */
  runs: Array<ReadableStreamDefaultController<Uint8Array>>;
  aborted: number;
}

let harness: Harness;
let gateway: FakeGatewayHandle;
const encoder = new TextEncoder();

function sseChunk(event: string, data: unknown) {
  return encoder.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

beforeEach(() => {
  harness = { queued: [], requested: [], runs: [], aborted: 0 };
  // Sessions/approvals come over WS (the fake gateway); only the queue itself
  // is still the REST bridge, so the app has to reach `ready` before it asks.
  const state = defaultState();
  state.sessions = [
    { id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' },
    { id: 's-2', title: '会话二', updatedAt: '2026-11-23T00:00:00Z' },
    { id: 's-3', title: '会话三', updatedAt: '2026-11-23T00:00:00Z' },
  ];
  state.messages = { 's-1': [], 's-2': [], 's-3': [] };
  gateway = installFakeGateway(state);
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);

    if (url.includes('/api/chat/run')) {
      const body = JSON.parse(String(init?.body ?? '{}')) as { text: string };
      harness.requested.push(body.text);
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          harness.runs.push(controller);
          controller.enqueue(sseChunk('message.delta', { turnId: 't', text: '工作中…' }));
          init?.signal?.addEventListener('abort', () => {
            harness.aborted += 1;
            controller.error(new DOMException('Aborted', 'AbortError'));
          });
        },
      });
      return new Response(stream, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      });
    }

    return new Response('not found', { status: 404 });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

/** Finish the oldest live run normally: usage + done, then close. */
async function finishRun() {
  const controller = harness.runs.shift();
  if (!controller) throw new Error('no live run');
  controller.enqueue(sseChunk('usage', { inputTokens: 1, outputTokens: 1 }));
  controller.enqueue(sseChunk('done', { turnId: 't', stopReason: 'end_turn' }));
  controller.close();
}

async function renderThread(sessionId: string) {
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={[`/c/${sessionId}`]}>
      <App />
    </MemoryRouter>,
  );
  const textbox = await screen.findByRole('textbox');
  return { user, textbox };
}

describe('prompt queue while a run is active', () => {
  it('queues two prompts, shows the count, then sends them in order and drains', async () => {
    const { user, textbox } = await renderThread('s-1');
    await waitFor(() => expect(textbox).not.toBeDisabled());

    await user.click(textbox);
    await user.keyboard('第一{Enter}');
    await waitFor(() => expect(harness.requested).toEqual(['第一']));

    await user.keyboard('第二{Enter}');
    await user.keyboard('第三{Enter}');
    expect(await screen.findByText('2 条待发送')).toBeInTheDocument();
    // Nothing was dropped: they are in the backend queue, not sent.
    expect(harness.requested).toEqual(['第一']);

    // The run settles → the head is claimed and sent automatically.
    await finishRun();
    await waitFor(() => expect(harness.requested).toEqual(['第一', '第二']));
    await waitFor(() => expect(screen.getByText('1 条待发送')).toBeInTheDocument());

    await finishRun();
    await waitFor(() => expect(harness.requested).toEqual(['第一', '第二', '第三']));
    await waitFor(() =>
      expect(screen.queryByLabelText('待发送队列')).not.toBeInTheDocument(),
    );
  }, 20_000);

  it('cancelling the first queued prompt sends only the second', async () => {
    const { user, textbox } = await renderThread('s-2');
    await waitFor(() => expect(textbox).not.toBeDisabled());

    await user.click(textbox);
    await user.keyboard('A{Enter}');
    await waitFor(() => expect(harness.requested).toEqual(['A']));
    await user.keyboard('B{Enter}');
    await user.keyboard('C{Enter}');
    expect(await screen.findByText('2 条待发送')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '取消待发送：B' }));
    await waitFor(() => expect(screen.getByText('1 条待发送')).toBeInTheDocument());
    expect(gateway.gateway.state.prompts['s-2']?.map((item) => item.text)).toEqual(['C']);

    await finishRun();
    await waitFor(() => expect(harness.requested).toEqual(['A', 'C']));
  }, 20_000);

  it('keeps the queue when the user aborts the run', async () => {
    const { user, textbox } = await renderThread('s-3');
    await waitFor(() => expect(textbox).not.toBeDisabled());

    await user.click(textbox);
    await user.keyboard('正在跑{Enter}');
    await waitFor(() => expect(harness.requested).toEqual(['正在跑']));
    await user.keyboard('稍后再发{Enter}');
    expect(await screen.findByText('1 条待发送')).toBeInTheDocument();

    // Esc stops this run through the arbiter (L5).
    (document.activeElement as HTMLElement | null)?.blur();
    document.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }),
    );
    await waitFor(() => expect(harness.aborted).toBe(1));

    // The abort must not silently discard what the user typed, and it must not
    // auto-start a new run either.
    expect(await screen.findByText('1 条待发送')).toBeInTheDocument();
    expect(harness.requested).toEqual(['正在跑']);

    // The explicit "发送队首" affordance resumes it.
    await user.click(screen.getByRole('button', { name: '发送队首' }));
    await waitFor(() => expect(harness.requested).toEqual(['正在跑', '稍后再发']));
  }, 20_000);
});