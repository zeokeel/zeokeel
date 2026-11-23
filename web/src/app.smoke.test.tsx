import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from './app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from './test/fake-gateway';

/**
 * Whole-tree smoke test: the real provider stack, the real antd/X components,
 * the real WS control-plane client (against an in-memory fake gateway) and the
 * real `XRequest` SSE parser — so the transcript path (send → stream → done) is
 * exercised without a gateway process.
 *
 * Visual layout and the browser's event loop are covered by the Playwright run
 * (`npm run e2e`), not here.
 */
const encoder = new TextEncoder();

function sseResponse(events: Array<[string, unknown]>): Response {
  const body = events.map(([name, data]) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`).join('');
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(body));
      controller.close();
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });

let handle: FakeGatewayHandle;

beforeEach(() => {
  // Data-plane: the fake gateway answers the WS methods the client really sends.
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '冒烟会话', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [], 's-2': [] };
  handle = installFakeGateway(state);

  // Run stream: still SSE (§4.6), so this one endpoint stays a fetch stub.
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    const url = String(input);
    if (/\/prompts$/.test(url)) return jsonResponse({ prompts: [] });
    if (url.includes('/api/chat/run')) {
      return sseResponse([
        ['reasoning.delta', { turnId: 't1', text: '想一下。' }],
        ['tool.start', { callId: 'c1', name: 'read_file', input: { path: 'a.ts' } }],
        ['tool.result', { callId: 'c1', ok: true, output: 'ok' }],
        ['message.delta', { turnId: 't1', text: '答案在这里。' }],
        ['usage', { inputTokens: 12, outputTokens: 3 }],
        ['done', { turnId: 't1', stopReason: 'end_turn' }],
      ]);
    }
    return new Response('not found', { status: 404 });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  handle.dispose();
});

describe('app shell', () => {
  it('renders the shell, the session list and the composer on a thread', async () => {
    render(
      <MemoryRouter initialEntries={['/c/s-1']}>
        <App />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('textbox')).toBeInTheDocument();
    expect(screen.getByRole('banner')).toBeInTheDocument();
    // The name shows twice on purpose: once as the sidebar row, once as the
    // top band title for the open thread.
    expect(await screen.findAllByText('冒烟会话')).toHaveLength(2);
    expect(screen.getByRole('link', { name: '跳到主内容' })).toBeInTheDocument();
  }, 15_000);

  it('sends a turn and renders the streamed answer with its tool step', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/c/s-1']}>
        <App />
      </MemoryRouter>,
    );

    const textbox = await screen.findByRole('textbox');
    await user.click(textbox);
    await user.keyboard('你好{Enter}');

    // The user's own message is shown immediately…
    expect(await screen.findByText('你好')).toBeInTheDocument();
    // …then the streamed answer, after reasoning + a tool node.
    expect(await screen.findByText('答案在这里。')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('read_file')).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText(/12 入 · 3 出/)).toBeInTheDocument());
  }, 15_000);

  it('creates a session from / and carries the first message into the thread (A2)', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    );

    const textbox = await screen.findByRole('textbox');
    await user.click(textbox);
    await user.keyboard('第一条消息{Enter}');

    // The new session appears in the sidebar and its message is streamed in the
    // thread the app navigated to.
    expect(await screen.findAllByText('新会话')).not.toHaveLength(0);
    expect(await screen.findByText('第一条消息')).toBeInTheDocument();
    expect(await screen.findByText('答案在这里。')).toBeInTheDocument();
  }, 15_000);
});