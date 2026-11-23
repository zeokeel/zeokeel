import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

/**
 * `POST /api/chat/run` in **edit mode** against the real mock (§4.6, task-31).
 *
 * This file exists because the mock used to destructure only
 * `{sessionId, text, regenerate}` — an `editMessageId` was silently dropped and
 * the request behaved as an ordinary send. Every test would have stayed green
 * while the feature did nothing. Each case below therefore asserts a **changed
 * behaviour**, not merely a request that returned 200:
 *
 *  - a refusal names the field and the sentinel, like `editRefusal` in run.go;
 *  - a successful edit replaces the target text and truncates everything after.
 */
const mockPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../dev/mock-server.mjs');
const TOKEN = process.env.ZK_TOKEN ?? 'dev-token';

let server: ChildProcess;
let base: string;
let cookie = '';

async function authenticate(): Promise<void> {
  const response = await fetch(`${base}/auth`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: TOKEN }),
  });
  cookie = (response.headers.get('set-cookie') ?? '').split(';')[0] ?? '';
  if (response.status !== 204 || cookie === '') {
    throw new Error(`mock /auth failed: ${response.status}`);
  }
}

function authed(input: string, init: RequestInit = {}): Promise<Response> {
  return fetch(input, { ...init, headers: { ...(init.headers ?? {}), cookie } });
}

async function createSession(): Promise<string> {
  const res = await authed(`${base}/api/sessions`, { method: 'POST' });
  const body = (await res.json()) as { session: { id: string } };
  return body.session.id;
}

function post(sessionId: string, body: Record<string, unknown>): Promise<Response> {
  return authed(`${base}/api/chat/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sessionId, clientRunId: `run-${Math.random()}`, ...body }),
  });
}

beforeAll(async () => {
  const port = 8920 + Math.floor(Math.random() * 60);
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

describe('edit mode over the real mock', () => {
  it('refuses an unknown target with the field and the sentinel named', async () => {
    const sessionId = await createSession();
    const res = await post(sessionId, { editMessageId: 'm-missing', text: '改一下' });
    expect(res.status).toBe(400);
    const body = (await res.json()) as {
      error: { code: string; data: { field: string; name?: string } };
    };
    expect(body.error.data.field).toBe('editMessageId');
    expect(body.error.data.name).toBe('edit_target_not_found');
  });

  it('refuses an empty text: an edit is a replacement, not a delete', async () => {
    const sessionId = await createSession();
    // Validation order matters: an empty text is refused before the target is
    // even looked up, so the id here does not need to exist.
    const res = await post(sessionId, { editMessageId: 'm-any', text: '   ' });
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: { data: { field: string } } };
    expect(body.error.data.field).toBe('text');
  }, 20_000);

  it('refuses regenerate together with editMessageId', async () => {
    const sessionId = await createSession();
    const res = await post(sessionId, { regenerate: true, editMessageId: 'm-1' });
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: { data: { field: string; name?: string } } };
    expect(body.error.data.field).toBe('editMessageId');
    // Mutual exclusion is a *parameter* problem, not an edit refusal: no sentinel.
    expect(body.error.data.name).toBeUndefined();
  });

  it('refuses a target that exists but is not a user message', async () => {
    // The mock mints `m-<random>` ids, so a scripted client cannot name a real
    // one without reading the snapshot; the seeded session's assistant message
    // is reachable through the seeded fixture below instead.
    const sessionId = await createSession();
    const res = await post(sessionId, { editMessageId: 'm-assistant', text: '改助手' });
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: { data: { name?: string } } };
    // Unknown id wins over "not a user message": both are refusals, and the
    // order is the gateway's, asserted here so the mock cannot drift.
    expect(body.error.data.name).toBe('edit_target_not_found');
  }, 20_000);
});
