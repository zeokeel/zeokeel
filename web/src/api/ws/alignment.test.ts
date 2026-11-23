import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { WS_PARAMS, validateParams } from '../../../dev/ws-params.mjs';

/**
 * Mock ↔ gateway alignment.
 *
 * The mock is a stand-in for the gateway, and a mock that is *more permissive*
 * than the real thing hides bugs (the fragment→cookie exchange was missing in
 * the client for exactly that reason: the mock never asked for a token). This
 * test pins the two surfaces together by reading both sources:
 *
 *  - every capability the gateway advertises must exist in the mock;
 *  - every method the client actually sends must be dispatched by the mock;
 *  - the mock must authenticate (`/auth` + cookie) like the gateway does.
 */

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '../../../..');

const goSource = readFileSync(resolve(repo, 'internal/gateway/methods.go'), 'utf8');
const mockSource = readFileSync(resolve(repo, 'web/dev/mock-server.mjs'), 'utf8');

/** The Go `serverCapabilities = []string{...}` block. */
function gatewayCapabilities(): string[] {
  const block = /var serverCapabilities = \[\]string\{([\s\S]*?)\n\}/.exec(goSource)?.[1] ?? '';
  return [...block.matchAll(/"([^"]+)"/g)].map((match) => match[1]);
}

/** The mock's `WS_CAPABILITIES` array. */
function mockCapabilities(): string[] {
  const block = /const WS_CAPABILITIES = \[([\s\S]*?)\n\];/.exec(mockSource)?.[1] ?? '';
  return [...block.matchAll(/'([^']+)'/g)].map((match) => match[1]);
}

/** Every method the mock's dispatcher handles (`case 'x':` or `if (method === 'x')`). */
function mockMethods(): string[] {
  const cases = [
    ...mockSource.matchAll(/case '([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9]+)*)':/g),
  ].map((match) => match[1]);
  const branches = [
    ...mockSource.matchAll(/method === '([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9]+)*)'/g),
  ].map((match) => match[1]);
  return [...cases, ...branches];
}

/** Methods the client sends (both directions are checked against the mock). */
const CLIENT_METHODS = [
  'initialize',
  'ready',
  'ping',
  'session.list.snapshot',
  'session.snapshot',
  'session.subscribe',
  'session.unsubscribe',
  'session.create',
  'approvals.snapshot',
  'approval.resolve',
  'settings.snapshot',
  'settings.set',
  'workspace.list.snapshot',
  'ask.snapshot',
  'plan.snapshot',
  'artifacts.snapshot',
  'run.cancel',
  'prompts.snapshot',
  'prompts.subscribe',
  'prompts.enqueue',
  'prompts.remove',
  'foreground.set',
  'tasks.snapshot',
  'tasks.subscribe',
  'task.status',
  'task.delete',
];

describe('mock ↔ gateway', () => {
  it('finds both capability lists (the regexes must not go stale)', () => {
    expect(gatewayCapabilities().length).toBeGreaterThan(5);
    expect(mockCapabilities().length).toBeGreaterThan(5);
    expect(mockMethods().length).toBeGreaterThan(10);
  });

  it('advertises every gateway capability in the mock', () => {
    const mock = new Set(mockCapabilities());
    const missing = gatewayCapabilities().filter((name) => !mock.has(name));
    expect(missing).toEqual([]);
  });

  it('dispatches every method the client sends', () => {
    const handled = new Set(mockMethods());
    const missing = CLIENT_METHODS.filter((name) => !handled.has(name));
    expect(missing).toEqual([]);
  });

  it('declares a params contract for every method the client sends', () => {
    const missing = CLIENT_METHODS.filter((name) => !(name in WS_PARAMS));
    expect(missing).toEqual([]);
    // Every declared method must also be dispatched, or the table is fiction.
    const handled = new Set(mockMethods());
    const undispatched = Object.keys(WS_PARAMS).filter((name) => !handled.has(name));
    expect(undispatched).toEqual([]);
  });

  it('rejects an unknown or missing parameter instead of ignoring it', () => {
    // The `{text}` vs `{prompt}` mismatch that reached the real gateway once.
    expect(
      validateParams('prompts.enqueue', { sessionId: 's-1', text: 'wrong field' }),
    ).toEqual({ ok: false, field: 'text', reason: 'unknown parameter' });
    expect(validateParams('prompts.enqueue', { sessionId: 's-1' })).toEqual({
      ok: false,
      field: 'prompt',
      reason: 'is required',
    });
    expect(validateParams('prompts.enqueue', { sessionId: 's-1', prompt: 'ok' })).toEqual({
      ok: true,
    });
  });

  it('authenticates like the gateway (§3.1): token exchange and a guarded WS', () => {
    // POST /auth sets the HttpOnly cookie …
    expect(mockSource).toMatch(/req\.url === '\/auth' && req\.method === 'POST'/);
    expect(mockSource).toMatch(/HttpOnly; SameSite=Strict; Path=\//);
    // … and the upgrade refuses without it, with a real 401.
    expect(mockSource).toMatch(/if \(!authorized\(req\)\)/);
    expect(mockSource).toMatch(/HTTP\/1\.1 401 Unauthorized/);
  });
});