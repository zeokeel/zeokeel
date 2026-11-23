/**
 * Surfaces that the mock gateway serves and the real gateway does not (yet).
 *
 * These are NOT protocol: they exist so offline development (`npm run mock`)
 * has data for screens whose gateway half has not landed. Every screen using
 * them renders its error state when the endpoints are absent, and none of them
 * is on the S2b acceptance path (session list / transcript / approvals /
 * settings / workspaces / ask / plan / artifacts are all on WS).
 *
 * Keeping them in one file makes the remaining gap countable: delete this file
 * when the last surface here has a real method.
 */

export interface MemoryEntry {
  id: string;
  text: string;
  createdAt?: string;
}

export interface ChildSession {
  id: string;
  parentId: string;
  title: string;
  messages: Array<{ id: string; role: string; text: string }>;
}

async function readJson<T>(res: Response, what: string): Promise<T> {
  if (!res.ok) throw new Error(`${what} failed: ${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

export async function loadMemory(sessionId: string): Promise<MemoryEntry[]> {
  const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/memory`, {
    headers: { Accept: 'application/json' },
  });
  const body = await readJson<{ entries: MemoryEntry[] }>(res, 'session memory');
  return body.entries;
}

export async function loadChildSession(
  sessionId: string,
  childId: string,
): Promise<ChildSession> {
  const res = await fetch(
    `/api/sessions/${encodeURIComponent(sessionId)}/children/${encodeURIComponent(childId)}`,
    { headers: { Accept: 'application/json' } },
  );
  return readJson<ChildSession>(res, 'child session');
}
