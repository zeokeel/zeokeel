import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { WS_PARAMS, validateParams } from '../../../dev/ws-params.mjs';
import { CONDITIONAL_CAPABILITIES, STATIC_CAPABILITIES } from '@/design/ws-contract';

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

/**
 * What the mock advertises: the contract's static list plus the conditional
 * halves it declares it serves. Both come from the generated contract, so this
 * reads the *declaration* rather than a literal array.
 */
function mockCapabilities(): string[] {
  const served = /const MOCK_CONDITIONAL = \[([^\]]*)\]/.exec(mockSource)?.[1] ?? '';
  const conditional = [...served.matchAll(/'([^']+)'/g)].map((match) => match[1]);
  return [...STATIC_CAPABILITIES, ...conditional];
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
/** Read the capability list the mock advertises (now derived, not literal). */
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
  'run.interject',
  'prompts.snapshot',
  'prompts.subscribe',
  'prompts.enqueue',
  'prompts.remove',
  'foreground.set',
  'context.snapshot',
  'context.subscribe',
  'team.snapshot',
  'team.spawn',
  'team.send',
  'team.wait',
  'team.interrupt',
  'team.close',
  'team.activity',
  'tasks.snapshot',
  'tasks.subscribe',
  'task.status',
  'task.delete',
  // §5.1.13: the inbound work inbox is the one orchestration half the web
  // client sends today (`api/inbox.ts`). Leases, approval tokens, notify routes,
  // webhook sources and `lead.run` are served by the mock already but no screen
  // sends them yet — they join this list with the screen that calls them.
  'inbox.snapshot',
  'inbox.dispatch',
  'inbox.triage',
];

describe('mock ↔ gateway', () => {
  it('finds both capability lists (the regexes must not go stale)', () => {
    expect(gatewayCapabilities().length).toBeGreaterThan(5);
    expect(mockCapabilities().length).toBeGreaterThan(5);
    expect(mockMethods().length).toBeGreaterThan(10);
  });

  it('has a contract that matches the gateway\'s own capability block', () => {
    // The generated contract is a copy of what the gateway declares; if the Go
    // list changes and `go generate` is not re-run, this is the failure that
    // says so — before a fixture drifts from production.
    const contract = new Set<string>(STATIC_CAPABILITIES);
    const missingInContract = gatewayCapabilities().filter((name) => !contract.has(name));
    const invented = [...contract].filter((name) => !gatewayCapabilities().includes(name));
    expect(missingInContract, 'contract.json 落后于 methods.go，请 go generate ./internal/gateway').toEqual(
      [],
    );
    expect(invented, 'contract.json 里有 methods.go 没有的能力').toEqual([]);
  });

  it('advertises every gateway capability in the mock', () => {
    const mock = new Set(mockCapabilities());
    const missing = gatewayCapabilities().filter((name) => !mock.has(name));
    expect(missing).toEqual([]);
  });

  it('names every conditional capability the contract declares, in the fixture', () => {
    // A conditional half the fixture fakes must be spelled with the contract's
    // own name, and every name the fixture lists must exist in the contract.
    for (const name of mockCapabilities().slice(STATIC_CAPABILITIES.length)) {
      expect(
        CONDITIONAL_CAPABILITIES.some((item) => item.name === name),
        `${name} 不在 contract.json 的 conditional 里`,
      ).toBe(true);
    }
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