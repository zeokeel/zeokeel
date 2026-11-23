/**
 * The fragment → cookie exchange (docs/TRANSPORT.md §3.1).
 *
 * The gateway prints `http://127.0.0.1:<port>/#token=<T>`. A fragment is never
 * sent to the server (not in the request line, logs or Referer), so the token
 * arrives with the page; the client must then
 *
 *   POST /auth {token}      → Set-Cookie: zk=<T>; HttpOnly; SameSite=Strict
 *   history.replaceState()  → erase the fragment from the address bar and history
 *
 * and only *then* open the WebSocket, which authenticates by cookie. Doing it in
 * the other order is a guaranteed 401 — the browser has no cookie yet.
 */

export type AuthOutcome =
  /** No `#token=` in the URL: a cookie is already expected to be present. */
  | 'none'
  /** The token was exchanged; the cookie is now set and the fragment is gone. */
  | 'exchanged'
  /** The gateway rejected the token (401/403): the link is stale or wrong. */
  | 'invalid';

export interface AuthOptions {
  fetchImpl?: typeof fetch;
  /** Injected for tests: the URL to read the fragment from. */
  location?: { hash: string; pathname: string; search: string };
  /** Injected for tests: erases the fragment. */
  replaceState?: (url: string) => void;
}

/** Pull `token` out of a fragment (`#token=x`, `#/path?token=x`). */
export function tokenFromHash(hash: string): string | null {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  if (raw === '') return null;
  const params = new URLSearchParams(raw.startsWith('/') ? raw.slice(raw.indexOf('?') + 1) : raw);
  const token = params.get('token');
  return token && token.trim() !== '' ? token.trim() : null;
}

/**
 * Exchange the fragment token for the session cookie, then erase the fragment.
 * Throws only when the gateway could not be reached (a network fault); a
 * rejected token resolves to `'invalid'` so the caller can show a readable
 * message and stop reconnecting.
 */
export async function exchangeFragmentToken(options: AuthOptions = {}): Promise<AuthOutcome> {
  const view =
    options.location ??
    (typeof window !== 'undefined'
      ? { hash: window.location.hash, pathname: window.location.pathname, search: window.location.search }
      : { hash: '', pathname: '/', search: '' });

  const token = tokenFromHash(view.hash);
  if (!token) return 'none';

  const doFetch = options.fetchImpl ?? fetch;
  const response = await doFetch('/auth', {
    method: 'POST',
    // Same-origin so the Set-Cookie the gateway returns is stored even though
    // the request is not a navigation.
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ token }),
  });

  if (response.status === 401 || response.status === 403) {
    return 'invalid';
  }
  if (!response.ok && response.status !== 204) {
    throw new Error(`授权交换失败：HTTP ${response.status}`);
  }

  // Erase the fragment: it must not stay in the address bar or in history
  // (a screenshot or a shared URL would leak the process token).
  const replace =
    options.replaceState ??
    ((url: string) => window.history.replaceState(null, '', url));
  replace(`${view.pathname}${view.search}`);
  return 'exchanged';
}

/**
 * Is the cookie still good?
 *
 * It has to be a **POST**: checkOrigin (§3.2/C3) requires an `Origin` header or
 * a Bearer token, and browsers only send `Origin` on non-GET requests. An
 * unknown `/api/*` path is the cheapest side-effect-free choice — the gateway
 * authenticates first and then answers 404; unauthenticated it answers 401.
 */
export async function probeAuthorization(fetchImpl: typeof fetch = fetch): Promise<
  'authorized' | 'unauthorized' | 'unknown'
> {
  try {
    const response = await fetchImpl('/api/ping-nonexistent', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: '{}',
    });
    if (response.status === 401 || response.status === 403) return 'unauthorized';
    // 404 means the request got past auth (the route simply does not exist);
    // 400 is past auth too (it failed on params, not on credentials).
    if (response.status === 404 || response.status === 400 || response.ok) {
      return 'authorized';
    }
    return 'unknown';
  } catch {
    return 'unknown';
  }
}