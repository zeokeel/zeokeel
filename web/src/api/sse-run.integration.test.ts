import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { XRequest } from '@ant-design/x-sdk';
import type { SSEOutput } from '@ant-design/x-sdk';
import { createRunRequest } from './run-stream';
import { applyChunk } from '@/runtime/gateway-chat-provider';
import { assistantMessage } from '@/chat/types';

/**
 * End-to-end over the real transport: the stock `XRequest` against the actual
 * mock gateway (`web/dev/mock-server.mjs`). This is the closest thing to the
 * browser check that exists without a headless browser — it proves the SSE
 * framing, the seven event types, their ordering, and client-side abort.
 */
const mockPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../dev/mock-server.mjs');
/** Non-browser clients authenticate with a Bearer token (§3.1), not a cookie. */
const MOCK_TOKEN = process.env.ZK_TOKEN ?? 'dev-token';
const bearer = { Authorization: `Bearer ${MOCK_TOKEN}` };

let server: ChildProcess;
let base: string;
/** The mock authenticates like the gateway: /api needs the `zk` cookie. */
let cookie = '';

async function authenticate(): Promise<void> {
  const response = await fetch(`${base}/auth`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: MOCK_TOKEN }),
  });
  const setCookie = response.headers.get('set-cookie') ?? '';
  cookie = setCookie.split(';')[0] ?? '';
  if (response.status !== 204 || cookie === '') {
    throw new Error(`mock /auth failed: ${response.status} ${setCookie}`);
  }
}

/** fetch with the session cookie — the browser does this implicitly. */
function authed(input: string, init: RequestInit = {}): Promise<Response> {
  return fetch(input, { ...init, headers: { ...(init.headers ?? {}), cookie } });
}

async function createSession(): Promise<string> {
  const res = await authed(`${base}/api/sessions`, { method: 'POST' });
  const body = (await res.json()) as { session: { id: string } };
  return body.session.id;
}

beforeAll(async () => {
  const port = 8800 + Math.floor(Math.random() * 120);
  server = spawn(process.execPath, [mockPath], {
    env: { ...process.env, PORT: String(port) },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  await once(server.stdout!, 'data');
  base = `http://127.0.0.1:${port}`;
  await authenticate();
}, 20_000);

afterAll(() => {
  server.kill();
});

describe('POST /api/chat/run over the real mock gateway', () => {
  it(
    'streams a full turn: reasoning → tool → answer → usage → done',
    async () => {
      const sessionId = await createSession();
      const chunks: SSEOutput[] = [];
      let settle: { resolve: () => void; reject: (error: Error) => void } | undefined;
      const finished = new Promise<void>((resolvePromise, rejectPromise) => {
        settle = { resolve: resolvePromise, reject: rejectPromise };
      });

      const request = XRequest(`${base}/api/chat/run`, {
        manual: true,
        headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
        callbacks: {
          onUpdate: (chunk) => {
            chunks.push(chunk);
          },
          onSuccess: () => settle?.resolve(),
          onError: (error) => settle?.reject(error),
        },
      });

      request.run({ sessionId, text: '解释一下 transport 层', clientRunId: 'run-e2e' });

      // The run parks on a tool call that needs a human decision; the approval
      // queue is the bridge the card and the Inbox both read.
      await waitFor(() => chunks.some((chunk) => chunk.event === 'tool.start'));
      expect(chunks.some((chunk) => chunk.event === 'tool.start')).toBe(true);
      const queued = (await (await authed(`${base}/api/approvals`)).json()) as {
        approvals: Array<{ id: string }>;
      };
      expect(queued.approvals.length).toBeGreaterThan(0);
      for (const request of queued.approvals) {
        await authed(`${base}/api/approvals/${request.id}/resolve`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ decision: 'allow' }),
        });
      }

      await finished;

      await finished;
      const names = chunks.map((chunk) => chunk.event);
      expect(names).toContain('reasoning.delta');
      expect(names).toContain('tool.start');
      expect(names).toContain('tool.result');
      expect(names).toContain('message.delta');
      expect(names).toContain('usage');
      expect(names[names.length - 1]).toBe('done');
      // §4.6 ordering guarantee: a call's start precedes its result, and the
      // parked run only continues after the decision.
      expect(names.indexOf('tool.start')).toBeLessThan(names.indexOf('tool.result'));

      const message = chunks.reduce(
        (accumulated, chunk) => applyChunk(accumulated, chunk),
        assistantMessage(),
      );
      expect(message.reasoning.length).toBeGreaterThan(0);
      expect(message.steps).toHaveLength(1);
      expect(message.steps[0]).toMatchObject({ name: 'bash', status: 'ok' });
      expect(message.text).toContain('mock 网关');
      expect(message.text).toContain('parseRunEvent');
      expect(message.usage).toMatchObject({ inputTokens: 1280, outputTokens: 320 });
      expect(message.stopReason).toBe('end_turn');
      expect(message.unknownEvents).toBeUndefined();

      // The turn joined the snapshot the client reloads after a refresh.
      const snapshot = await authed(`${base}/api/sessions/${sessionId}`);
      const body = (await snapshot.json()) as { messages: Array<{ role: string; text: string }> };
      expect(body.messages.at(0)).toMatchObject({ role: 'user', text: '解释一下 transport 层' });
      expect(body.messages.at(-1)?.text).toContain('mock 网关');
    },
    30_000,
  );

  it(
    'aborts a live turn when the client stops it',
    async () => {
      const sessionId = await createSession();
      const received: SSEOutput[] = [];
      let settle: (error: Error) => void = () => {};
      const errored = new Promise<Error>((resolvePromise) => {
        settle = resolvePromise;
      });

      const request = createRunRequest(`${base}/api/chat/run`, bearer);
      request.options.callbacks = {
        onUpdate: (chunk) => {
          received.push(chunk);
        },
        onSuccess: () => settle(new Error('stream completed instead of aborting')),
        onError: (error) => settle(error),
      };

      request.run({ sessionId, text: '写一段长一点的东西', clientRunId: 'run-abort' });

      // Wait until the stream is genuinely flowing, then stop it.
      await waitFor(() => received.length >= 2);
      request.abort();

      const error = await errored;
      expect(error.name).toBe('AbortError');

      // Whatever already arrived is preserved for the provider: the partial
      // answer plus a neutral stop, never an error bubble.
      const message = received.reduce(
        (accumulated, chunk) => applyChunk(accumulated, chunk),
        assistantMessage(),
      );
      expect(message.error).toBeUndefined();
      // The stream had started (reasoning/tool events), so the partial row the
      // provider keeps is non-empty.
      expect(message.reasoning.length + message.text.length).toBeGreaterThan(0);
    },
    30_000,
  );

  it(
    'surfaces a terminal error event as a message error',
    async () => {
      const sessionId = await createSession();
      const chunks: SSEOutput[] = [];
      let settle: { resolve: () => void; reject: (error: Error) => void } | undefined;
      const finished = new Promise<void>((resolvePromise, rejectPromise) => {
        settle = { resolve: resolvePromise, reject: rejectPromise };
      });

      const request = createRunRequest(`${base}/api/chat/run`, bearer);
      request.options.callbacks = {
        onUpdate: (chunk) => {
          chunks.push(chunk);
        },
        onSuccess: () => settle?.resolve(),
        onError: (error) => settle?.reject(error),
      };

      request.run({ sessionId, text: '请触发 error 分支', clientRunId: 'run-error' });
      await finished;

      expect(chunks.map((chunk) => chunk.event)).toEqual(['error']);
      const message = chunks.reduce(
        (accumulated, chunk) => applyChunk(accumulated, chunk),
        assistantMessage(),
      );
      expect(message.error?.code).toBe('provider_unavailable');
    },
    30_000,
  );
});

async function waitFor(predicate: () => boolean, timeout = 10_000): Promise<void> {
  const started = Date.now();
  while (!predicate()) {
    if (Date.now() - started > timeout) throw new Error('timed out waiting for the stream');
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
  }
}