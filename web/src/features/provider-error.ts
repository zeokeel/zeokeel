/**
 * Provider failures, in the user's language.
 *
 * v0.3.0 showed the model client's own words verbatim — an English JSON body
 * wrapped in our internal code, inline in a Chinese conversation. Two real
 * reports from the Linux build drive this classifier:
 *
 *   401 + "You didn't provide an API key"        → noCredential
 *   dial tcp …: i/o timeout (the 30 s blackhole) → unreachable
 *
 * The original text is never thrown away: it goes to a collapsed "details"
 * panel, because it is the only thing that says *which* endpoint failed. The
 * classifier only decides what the first line says and what the user can do.
 */

export type ProviderFailureKind = 'noCredential' | 'unauthorized' | 'unreachable' | 'unknown';

export interface ProviderFailure {
  kind: ProviderFailureKind;
  /** Our code's words, licence intact — shown in the collapsed details panel. */
  detail: string;
  /** `provider "openai"` → `openai`, when the message names one. */
  provider?: string;
}

/**
 * Our internal JSON-RPC code, appended by the client as `（-32603）` (full-width
 * parentheses). Only that exact shape at the end is removed: a `(-21)` inside
 * prose is part of the message, not an internal code.
 */
const TRAILING_CODE = /（-\d+）\s*$/;

const PROVIDER_NAME = /provider "([^"]+)"/;

const NO_CREDENTIAL = /api key/i;
const UNAUTHORIZED = /\b401\b|unauthorized/i;
const UNREACHABLE =
  /i\/o timeout|dial tcp|connection refused|no such host|network is unreachable|context deadline exceeded/i;

export function classifyProviderFailure(text: string): ProviderFailure {
  const detail = text.replace(TRAILING_CODE, '').trim();
  const provider = PROVIDER_NAME.exec(detail)?.[1];

  const withProvider = (kind: ProviderFailureKind): ProviderFailure =>
    provider ? { kind, detail, provider } : { kind, detail };

  // "401 + a message about the key" is a missing credential; a 401 without that
  // phrasing is a credential the service rejected — different advice ("check the
  // key you typed" vs "your key is not valid any more").
  if (UNAUTHORIZED.test(detail) && NO_CREDENTIAL.test(detail)) return withProvider('noCredential');
  if (UNAUTHORIZED.test(detail)) return withProvider('unauthorized');
  if (UNREACHABLE.test(detail)) return withProvider('unreachable');
  return withProvider('unknown');
}