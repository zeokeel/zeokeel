import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { App } from '@/app';
import { SETTING_KEYS } from '@/design/settings-keys';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';
import { localeFromTag, resolveLocale, translate } from '@/i18n';

/**
 * B5 — 中英双语 (task-22).
 *
 * Three things must hold:
 *  1. the language comes from the `settings` domain row (`ui.language`) and a
 *     delta switches the UI without a reload;
 *  2. with no row, `navigator.language` decides (`zh*` → 中文, else English);
 *  3. no user-visible Chinese is hard-coded outside `src/i18n`, or the next
 *     component silently reintroduces the monolingual client.
 */

let gateway: FakeGatewayHandle | null = null;

beforeEach(() => {
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway?.dispose();
  gateway = null;
});

function renderApp() {
  render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  );
}

describe('locale resolution', () => {
  it('prefers the stored row, then the browser tag', () => {
    expect(resolveLocale('en', 'zh-CN')).toBe('en');
    expect(resolveLocale('zh', 'en-US')).toBe('zh');
    expect(localeFromTag('zh-Hant-TW')).toBe('zh');
    expect(localeFromTag('en-GB')).toBe('en');
    // Unknown/absent tags fall back to Chinese, the product's first language.
    expect(localeFromTag(undefined)).toBe('zh');
    expect(localeFromTag('fr-FR')).toBe('en');
  });

  it('interpolates {name} placeholders and leaves unknown ones alone', () => {
    expect(translate('zh', 'inbox.count', { count: 3 })).toBe('3 项等待你');
    expect(translate('en', 'inbox.count', { count: 3 })).toBe('3 waiting for you');
    expect(translate('en', 'shell.approvalsBadge', {})).toBe('{count} waiting');
  });
});

describe('B5 — the settings row drives the copy', () => {
  it('switches the UI to English when the row changes, without a reload', async () => {
    const state = defaultState();
    gateway = installFakeGateway(state);
    renderApp();

    // Chinese first (the row says zh).
    expect(await screen.findByRole('menuitem', { name: '团队' })).toBeInTheDocument();

    // The gateway pushes a delta; the shell re-renders in place.
    state.settings[SETTING_KEYS.uiLanguage] = 'en';
    gateway?.gateway.notify('settings.delta', {
      baseRev: 0,
      rev: 99,
      rows: { [SETTING_KEYS.uiLanguage]: 'en' },
      removed: [],
    });

    await waitFor(() =>
      expect(screen.getByRole('menuitem', { name: 'Team' })).toBeInTheDocument(),
    );
    expect(screen.queryByRole('menuitem', { name: '团队' })).toBeNull();
    // The URL is unchanged: this is a re-render, not a navigation.
    expect(window.location.pathname).toBe('/');
  });

  it('falls back to navigator.language when the row is missing', async () => {
    const state = defaultState();
    delete state.settings[SETTING_KEYS.uiLanguage];
    gateway = installFakeGateway(state);
    const original = navigator.language;
    Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
    renderApp();

    expect(await screen.findByRole('menuitem', { name: 'Team' })).toBeInTheDocument();
    Object.defineProperty(navigator, 'language', { configurable: true, value: original });
  });
});

/**
 * No hard-coded Chinese outside `src/i18n`.
 *
 * A file may still be listed below while it is being converted; the list is the
 * budget and it may only shrink — a *new* file with CJK fails immediately,
 * which is the case this guards (someone writes copy straight into JSX).
 */
const PENDING_CONVERSION = new Set([
  // Secondary screens still holding inline Chinese. This list is the remaining
  // B5 budget: removing an entry is progress, adding one is a regression, and a
  // file that no longer has CJK must be removed (the test below enforces it).
  'api/settings.ts',
  'features/settings/settings-screen.tsx',
  // Diagnostics, not copy: these strings surface only when something is already
  // broken, and translating them would hide the cause from a bug report.
  'api/gateway.ts',
  'api/ws/auth.ts',
]);

const here = dirname(fileURLToPath(import.meta.url));
const sourceRoot = resolve(here, '..');
const CJK = /[\u4e00-\u9fff]/;

function tsxFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...tsxFiles(full));
      continue;
    }
    if (!/\.(ts|tsx)$/.test(entry)) continue;
    out.push(full);
  }
  return out;
}

/** Text that reaches the user: JSX text, string literals, aria-labels. */
function userVisibleChinese(text: string): string[] {
  const hits: string[] = [];
  // Comments are documentation, not copy: drop block comments and JSDoc first.
  const withoutBlocks = text.replace(/\/\*[\s\S]*?\*\//g, (block) =>
    block.replace(/[^\n]/g, ''),
  );
  withoutBlocks.split('\n').forEach((line, index) => {
    if (!CJK.test(line)) return;
    // Comments are documentation, not copy.
    const code = line.replace(/\/\/.*$/, '');
    if (!CJK.test(code)) return;
    hits.push(`${index + 1}: ${line.trim().slice(0, 80)}`);
  });
  return hits;
}

describe('B5 — copy lives in the catalogs', () => {
  it('has no unexplained Chinese outside src/i18n', () => {
    const offenders: string[] = [];
    for (const file of tsxFiles(sourceRoot)) {
      const relative = file.slice(sourceRoot.length + 1);
      if (relative.startsWith('i18n/')) continue;
      // Test fixtures are not shipped copy (a session title in a fake gateway).
      if (relative.startsWith('test/')) continue;
      if (/\.test\.(ts|tsx)$/.test(relative)) continue;
      if (PENDING_CONVERSION.has(relative)) continue;
      const hits = userVisibleChinese(readFileSync(file, 'utf8'));
      if (hits.length > 0) offenders.push(`${relative}:${hits[0]}`);
    }
    expect(offenders).toEqual([]);
  });

  it('keeps the pending list honest: every entry still exists and still has CJK', () => {
    const stale = [...PENDING_CONVERSION].filter((relative) => {
      const full = resolve(sourceRoot, relative);
      try {
        return !CJK.test(readFileSync(full, 'utf8'));
      } catch {
        return true;
      }
    });
    // A converted file must be removed from the list, so the budget shrinks.
    expect(stale).toEqual([]);
  });
});