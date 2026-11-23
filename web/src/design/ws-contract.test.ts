import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { CONDITIONAL_CAPABILITIES, STATIC_CAPABILITIES, WS_METHODS, WS_PARAMS } from './ws-contract';

/**
 * The generated contract must still describe `internal/gateway/contract.json`.
 *
 * `ws-contract.ts` is generated, so the failure mode is staleness: the gateway
 * adds a method, nobody re-runs `go generate ./internal/gateway && npm run
 * gen:ws-contract`, and every fixture keeps validating the old shape while the
 * suite stays green. This compares the two files directly — no spawn, no cache.
 */
const here = dirname(fileURLToPath(import.meta.url));
const CONTRACT = resolve(here, '../../../internal/gateway/contract.json');

interface ContractFile {
  protocolVersion?: number;
  params: Record<string, { required?: string[]; optional?: string[] }>;
  capabilities: {
    static: string[];
    conditional: Array<{ name: string; when: string }>;
  };
}

const contract = JSON.parse(readFileSync(CONTRACT, 'utf8')) as ContractFile;

describe('generated WS contract', () => {
  it('lists exactly the gateway\'s methods', () => {
    expect([...WS_METHODS].sort()).toEqual(Object.keys(contract.params).sort());
  });

  it('matches the gateway\'s parameter rules method by method', () => {
    for (const method of WS_METHODS) {
      const expected = contract.params[method] ?? {};
      expect(WS_PARAMS[method].required, `${method}.required`).toEqual(expected.required ?? []);
      expect(WS_PARAMS[method].optional, `${method}.optional`).toEqual(expected.optional ?? []);
    }
  });

  it('copies the static capability list verbatim', () => {
    expect([...STATIC_CAPABILITIES]).toEqual(contract.capabilities.static);
  });

  it('copies the conditional capabilities with the service that provides them', () => {
    expect(CONDITIONAL_CAPABILITIES.map((item) => ({ ...item }))).toEqual(
      contract.capabilities.conditional.map((item) => ({ name: item.name, when: item.when })),
    );
  });

  it('says how to fix it when stale', () => {
    // The message is part of the contract for humans: the generator's error and
    // this file's header must point at the same command.
    const generated = readFileSync(resolve(here, './ws-contract.ts'), 'utf8');
    expect(generated).toContain('go generate ./internal/gateway');
    expect(generated).toContain('gen-ws-contract');
  });
});
