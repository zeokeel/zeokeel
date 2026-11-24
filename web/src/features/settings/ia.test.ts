import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { SETTING_KEYS } from '@/design/settings-keys';
import { CATALOGS } from '@/i18n';
import { SETTINGS_PAGES, orderedPages } from './ia';

/**
 * The IA and the generated key table must agree, both ways.
 *
 * `src/design/settings-keys.ts` is generated from `internal/contracts` (the
 * backend owns the row names); `ia.ts` is the product's page layout. Two lists
 * describing one fact will drift unless something asserts they are equal — the
 * failure modes are silent:
 *
 *  - a row the backend added but the IA never lists ⇒ a setting exists that no
 *    page shows, and no test notices;
 *  - a row the IA lists but the backend does not store ⇒ a control that writes
 *    nowhere.
 */

const here = dirname(fileURLToPath(import.meta.url));
const KEYS_FILE = resolve(here, '../../design/settings-keys.ts');

/** The row keys the IA actually renders. */
function iaRowKeys(): string[] {
  return SETTINGS_PAGES.flatMap((page) =>
    page.sections.flatMap((section) => section.fields.map((field) => field.key)),
  );
}

/** Every value in the generated table, read from source so a hand edit shows. */
function generatedKeys(): string[] {
  const source = readFileSync(KEYS_FILE, 'utf8');
  return [...source.matchAll(/^\s+\w+: '([^']+)',$/gm)].map((match) => match[1]);
}

/**
 * Rows the IA renders that the generator does not emit yet. Each one is a real
 * gateway row (JSON blob / toolkit / skills / mcp / web search), declared in
 * `internal/contracts` but outside `settingskeys.go`.
 */
const EXTRA_ROWS = new Set([
  'modelcatalog.models',
  'presets.agents',
  'sandbox.writableDirs',
  'toolkit.shellTimeoutSeconds',
  'toolkit.shellMaxOutputKiB',
  'toolkit.deniedGlobs',
  'ui.workSteps.mode',
  'ui.workSteps.collapse',
  'ui.sessions.showArchived',
  'tools.disabled',
  'skills.disabled',
  'skills.dirs',
  'mcp.servers',
  'mcp.disabledServers',
  'web.search.endpoint',
  'web.search.apiKey',
  'lsp.workspace_config',
]);

/**
 * Rows the backend declares but nothing reads (no `settings.Get` outside contracts).
 * They are not given a control: a field that writes a row nobody reads is exactly
 * the dishonest UI this file exists to prevent. The list is the visible debt.
 */
const UNREAD_ROWS = ['prompt.presets'];

describe('settings IA × generated SETTING_KEYS', () => {
  it('lists every generated row somewhere', () => {
    const listed = new Set(iaRowKeys());
    const missing = generatedKeys().filter((key) => !listed.has(key) && !UNREAD_ROWS.includes(key));
    expect(missing, 'backend 有、IA 没有（会出现"有值但没页面显示"的设置行）').toEqual([]);
  });

  it('invents no row the backend does not store', () => {
    const known = new Set(generatedKeys());
    const invented = iaRowKeys().filter((key) => !known.has(key) && !EXTRA_ROWS.has(key));
    expect(invented, 'IA 有、backend 没有（控件写不进任何地方）').toEqual([]);
  });

  it('keeps the extra rows honest: each is really stored by the gateway', () => {
    // Not every contract key reaches the generator yet (it covers
    // `internal/contracts/settingskeys.go`; the JSON-blob and toolkit rows are
    // declared elsewhere). This list is the visible boundary: every entry is a
    // row the Go side stores, and it shrinks as the generator grows.
    for (const key of EXTRA_ROWS) {
      expect(iaRowKeys(), `${key} 不在 IA 里`).toContain(key);
    }
  });

  it('reads a non-empty key table (the check is not silently empty)', () => {
    expect(generatedKeys().length).toBeGreaterThanOrEqual(30);
    expect(iaRowKeys().length).toBeGreaterThanOrEqual(30);
    expect(SETTING_KEYS.agentPermission).toBe('agent.permission');
  });
});

describe('settings IA copy is in the catalogue', () => {
  const keys = (() => {
    const out: string[] = [];
    for (const page of SETTINGS_PAGES) {
      out.push(page.labelKey, page.group.labelKey);
      for (const section of page.sections) {
        if (section.titleKey) out.push(section.titleKey);
        for (const field of section.fields) {
          out.push(field.labelKey);
          if (field.helpKey) out.push(field.helpKey);
          for (const option of field.options ?? []) out.push(option.labelKey);
        }
      }
    }
    return out;
  })();

  it('has every label in both languages', () => {
    for (const key of keys) {
      expect(CATALOGS.zh[key as keyof typeof CATALOGS.zh], `zh 缺 ${key}`).toBeTruthy();
      expect(CATALOGS.en[key as keyof typeof CATALOGS.en], `en 缺 ${key}`).toBeTruthy();
    }
  });

  it('gives pages distinct keys and a stable order', () => {
    const pageKeys = SETTINGS_PAGES.map((page) => page.key);
    expect(new Set(pageKeys).size).toBe(pageKeys.length);

    // Ordered by (group rank, page order) — never alphabetically: the IA order
    // is a product decision, so assert the comparator, not a sort of the names.
    const ordered = orderedPages();
    for (let i = 1; i < ordered.length; i += 1) {
      const previous = ordered[i - 1];
      const current = ordered[i];
      const byRank = previous.group.rank - current.group.rank;
      const byOrder = previous.order - current.order;
      expect(byRank < 0 || (byRank === 0 && byOrder <= 0), `${previous.key} 排在 ${current.key} 前`).toBe(
        true,
      );
    }
  });
});
