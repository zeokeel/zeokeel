/**
 * The prompt queue (A5/B13; mirrors contracts/promptqueue.go).
 *
 * A prompt sent while the session is running waits in the session's inbox
 * instead of being dropped. The backend owns the queue (durable, per session);
 * the client only enqueues, lists, cancels, and — because the run is
 * client-driven here (`POST /api/chat/run`, TRANSPORT §4.6) — claims the head
 * and issues the next run when the current one settles.
 */
import { authorizationReady } from './gateway';

export interface QueuedPrompt {
  id: string;
  sessionId: string;
  text: string;
  createdAt?: string;
}

/** Every call waits for the cookie: /auth must land before any /api request. */
async function authorized(): Promise<void> {
  await authorizationReady();
}

async function readJson<T>(res: Response, what: string): Promise<T> {
  if (!res.ok) throw new Error(`${what} failed: ${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

const queuePath = (sessionId: string) => `/api/sessions/${encodeURIComponent(sessionId)}/prompts`;

export async function listQueuedPrompts(sessionId: string): Promise<QueuedPrompt[]> {
  await authorized();
  const res = await fetch(queuePath(sessionId), { headers: { Accept: 'application/json' } });
  const body = await readJson<{ prompts: QueuedPrompt[] }>(res, 'prompt queue');
  return body.prompts;
}

export async function enqueuePrompt(sessionId: string, text: string): Promise<QueuedPrompt> {
  await authorized();
  const res = await fetch(queuePath(sessionId), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ text }),
  });
  const body = await readJson<{ prompt: QueuedPrompt }>(res, 'enqueue prompt');
  return body.prompt;
}

export async function removeQueuedPrompt(sessionId: string, promptId: string): Promise<void> {
  await authorized();
  const res = await fetch(`${queuePath(sessionId)}/${encodeURIComponent(promptId)}`, {
    method: 'DELETE',
    headers: { Accept: 'application/json' },
  });
  await readJson<{ ok: boolean }>(res, 'remove prompt');
}