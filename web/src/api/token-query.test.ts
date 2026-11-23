import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * The mock accepts `?token=` on its WebSocket handshake so scripted clients can
 * drive the control plane (a browser cannot set handshake headers). The **real**
 * gateway has no query-token path — `internal/gateway/auth.go` accepts a cookie
 * or `Authorization: Bearer` only.
 *
 * That makes it a test-fixture privilege, and privileges leak. This asserts the
 * convenience never reaches product code: `src/api/` must not put a token in a
 * query string, in any of the obvious shapes.
 */
const here = dirname(fileURLToPath(import.meta.url));
const apiRoot = resolve(here, '.');

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...sourceFiles(full));
      continue;
    }
    if (!/\.(ts|tsx)$/.test(entry)) continue;
    // Tests may *use* the mock's convenience; product code may not.
    if (/\.test\.(ts|tsx)$/.test(entry)) continue;
    out.push(full);
  }
  return out;
}

/** Strip comments: prose may mention `#token=` without sending it. */
function code(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
}

describe('query tokens stay in the mock', () => {
  it('never sends a token in a query string from src/api', () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(apiRoot)) {
      const body = code(readFileSync(file, 'utf8'));
      if (/\?token=/.test(body)) offenders.push(`${file}: ?token=`);
      if (/searchParams\.set\(\s*['"]token['"]/.test(body)) offenders.push(`${file}: searchParams.set(token)`);
      if (/['"]token=/.test(body)) offenders.push(`${file}: 'token=' literal`);
    }
    expect(offenders).toEqual([]);
  });

  it('reads the fragment (`#token=`) through the one documented helper', () => {
    // The fragment is the real mechanism (§3.1); it is parsed in `ws/auth.ts`
    // only, and then erased from the address bar.
    const auth = code(readFileSync(resolve(apiRoot, 'ws/auth.ts'), 'utf8'));
    expect(auth).toContain('tokenFromHash');
    expect(auth).toMatch(/replaceState/);
  });
});
