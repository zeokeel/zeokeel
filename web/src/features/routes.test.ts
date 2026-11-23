import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { matchPath } from 'react-router';
import { describe, expect, it } from 'vitest';
import { ROUTES, routeForPath } from './routes';

/**
 * The route table must be able to match itself (task-27).
 *
 * `cf5bb44` fixed a bug that had been live and green: the file route was
 * registered as `/c/:sessionId/files/*path`, and react-router only treats `*`
 * as a splat — a literal `*path` matches nothing, so **every file deep link fell
 * through to NotFound** while the whole suite stayed green, because no test ever
 * walked a real URL.
 *
 * Two layers, both driven from the data so a new route is covered the moment it
 * is added:
 *  1. every `ROUTES` entry gets a concrete sample URL and must match itself;
 *  2. the `<Route path>` list in `app-routes.tsx` must agree with `ROUTES` — the
 *     bug was "two places to change, one changed".
 */

/** `:param` → `sample`, `*` → `deep/path/here`; keeps the shape, fills it in. */
export function sampleUrlFor(pattern: string): string {
  const filled = pattern
    .split('/')
    .map((segment) => {
      // Any star-led segment is treated as the splat, so a malformed `*path`
      // gets a realistic URL and then fails to match — the bug cannot hide here.
      if (segment.startsWith('*')) return 'deep/path/here';
      if (segment.startsWith(':')) return segment.slice(1) === 'page' ? 'model' : 'sample';
      return segment;
    })
    .join('/');
  return filled === '' ? '/' : filled;
}

/** Paths registered in JSX. Read as text: the check must see what ships. */
export function jsxRoutePaths(source: string): string[] {
  return [...source.matchAll(/<Route\s+path="([^"]+)"/g)].map((match) => match[1]);
}

const here = dirname(fileURLToPath(import.meta.url));
const APP_ROUTES_SOURCE = readFileSync(resolve(here, 'app-routes.tsx'), 'utf8');

describe('every ROUTES entry matches its own URL', () => {
  it.each(ROUTES.map((route) => [route.ref, route.path] as const))(
    '%s (%s) matches a concrete sample URL',
    (ref, pattern) => {
      const sample = sampleUrlFor(pattern);
      const matched = matchPath({ path: pattern, end: true }, sample);
      expect(matched, `${ref} 的样例 URL ${sample} 没有被自己的 path 匹配`).toBeTruthy();

      // `routeForPath` resolves the same entry — this is the lookup the top band
      // and the shell actually use.
      const resolved = routeForPath(sample);
      expect(resolved?.ref, `${ref} 被 routeForPath 解析成了 ${resolved?.ref}`).toBe(ref);
    },
  );

  it('has no route whose sample URL collides with another entry', () => {
    const seen = new Map<string, string>();
    for (const route of ROUTES) {
      const sample = sampleUrlFor(route.path);
      const prior = seen.get(sample);
      expect(prior, `${route.ref} 与 ${prior} 的样例 URL 相同（${sample}）`).toBeUndefined();
      seen.set(sample, route.ref);
    }
  });

  it('accepts only the splat shape react-router understands', () => {
    const offenders: string[] = [];
    for (const route of ROUTES) {
      const stars = route.path.split('*').length - 1;
      if (stars > 1) offenders.push(`${route.ref}: 多个 splat（${route.path}）`);
      // `*path` / `*something` is the exact bug: it reads as a literal segment.
      if (/\*[A-Za-z]/.test(route.path)) offenders.push(`${route.ref}: 命名 splat（${route.path}）`);
      // `:a:b` in one segment cannot match anything either.
      for (const segment of route.path.split('/')) {
        if ((segment.match(/:/g) ?? []).length > 1) {
          offenders.push(`${route.ref}: 段内多个参数（${segment}）`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});

describe('app-routes.tsx and ROUTES agree', () => {
  const jsxPaths = jsxRoutePaths(APP_ROUTES_SOURCE);
  const tableePaths = ROUTES.map((route) => route.path);

  it('registers exactly the paths in the table (plus the 404 catch-all)', () => {
    const jsx = new Set(jsxPaths);
    const table = new Set(tableePaths);
    const missingInJsx = tableePaths.filter((path) => !jsx.has(path));
    // `/settings` is the bare redirect target and lives only in JSX.
    const missingInTable = [...jsx].filter((path) => path !== '*' && path !== '/settings' && !table.has(path));

    expect(missingInJsx, 'ROUTES 里有、app-routes.tsx 里没有（深链会 404）').toEqual([]);
    expect(missingInTable, 'app-routes.tsx 里有、ROUTES 里没有（导航/顶带不认识）').toEqual([]);
  });

  it('keeps the 404 catch-all registered', () => {
    expect(jsxPaths).toContain('*');
  });

  it('reads a non-empty route list (the check is not silently empty)', () => {
    // A parse failure would make the assertions above vacuously true.
    expect(jsxPaths.length).toBeGreaterThanOrEqual(ROUTES.length);
    expect(ROUTES.length).toBeGreaterThanOrEqual(15);
  });
});

describe('the 404 catch-all is reachable and specific', () => {
  it('does not match any real route', () => {
    const unknown = '/definitely/not/a/route';
    expect(routeForPath(unknown)).toBeUndefined();
    // …and the JSX catch-all is what renders it.
    expect(matchPath({ path: '*', end: true }, unknown)).toBeTruthy();
  });

  it('does not swallow real routes', () => {
    for (const route of ROUTES) {
      const sample = sampleUrlFor(route.path);
      expect(matchPath({ path: route.path, end: true }, sample)?.pathname).toBe(sample);
    }
  });
});