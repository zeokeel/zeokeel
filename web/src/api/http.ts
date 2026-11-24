/**
 * The one place a REST bridge waits for authorization.
 *
 * The client exchanges the `#token=` fragment for a cookie once per page load
 * (`POST /auth`). Every `/api/*` read model and upload authenticates with that
 * cookie, so a request issued before the exchange finishes collects a 401 that
 * looks exactly like a broken feature — file tree failing to load, an empty
 * model list, an upload that "just doesn't work" — until the page is refreshed.
 *
 * `authorizationReady()` existed for this and had no callers
 * (AUDIT-2026-11-24 A-8); the bridges below go through `authedFetch`, so the
 * rule holds in one place instead of in every call site.
 */
import { authorizationReady } from './gateway';

/** fetch() that resolves the fragment→cookie exchange first. */
export async function authedFetch(input: string, init?: RequestInit): Promise<Response> {
  await authorizationReady();
  return fetch(input, init);
}
